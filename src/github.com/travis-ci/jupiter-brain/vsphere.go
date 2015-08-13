package jupiterbrain

import (
	"fmt"
	"net/url"
	"reflect"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/pborman/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

type vSphereInstanceManager struct {
	log *logrus.Logger

	vSphereClientMutex sync.Mutex
	vSphereClient      *govmomi.Client

	vSphereURL *url.URL

	paths VSpherePaths
}

// VSpherePaths holds some vSphere inventory paths that are required for the
// InstanceManager to function properly.
type VSpherePaths struct {
	// BasePath is the path to a folder in which the base VMs that the
	// instance manager use to create VMs are. These VMs should have a
	// snapshot named "base", and new VMs will spin up from this snapshot.
	// The path should end with a slash.
	BasePath string

	// VMPath is the path to a folder in which VMs will be cloned into. Any
	// VM in this folder can be displayed and stopped with the instance
	// manager. The path should end with a slash.
	VMPath string

	// ClusterPath is the inventory path to a ClusterComputeResource that
	// is used as the resource pool for new VMs.
	ClusterPath string
}

// NewVSphereInstanceManager creates a new instance manager backed by vSphere
func NewVSphereInstanceManager(log *logrus.Logger, vSphereURL *url.URL, paths VSpherePaths) InstanceManager {
	return &vSphereInstanceManager{
		log:        log,
		vSphereURL: vSphereURL,
		paths:      paths,
	}
}

func (i *vSphereInstanceManager) Fetch(ctx context.Context, id string) (*Instance, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath+id)
	if err != nil {
		return nil, VSphereAPIError{UnderlyingError: err}
	}

	if vmRef == nil {
		return nil, VirtualMachineNotFoundError{Path: i.paths.VMPath, ID: id}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return nil, fmt.Errorf("object at path %s is a %T, but expected VirtualMachine", i.paths.VMPath+id, vmRef)
	}

	return i.instanceForVirtualMachine(ctx, vm)
}

func (i *vSphereInstanceManager) List(ctx context.Context) ([]*Instance, error) {
	folder, err := i.vmFolder(ctx)
	if err != nil {
		return nil, err
	}

	vms, err := folder.Children(ctx)
	if err != nil {
		return nil, VSphereAPIError{UnderlyingError: err}
	}

	var instances []*Instance
	for _, vmRef := range vms {
		vm, ok := vmRef.(*object.VirtualMachine)
		if !ok {
			continue
		}

		instance, err := i.instanceForVirtualMachine(ctx, vm)
		if err != nil {
			return nil, err
		}

		instances = append(instances, instance)
	}

	return instances, nil
}

func (i *vSphereInstanceManager) Start(ctx context.Context, baseName string) (*Instance, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	vm, snapshotTree, err := i.findBaseVMAndSnapshot(ctx, baseName)
	if err != nil {
		return nil, fmt.Errorf("couldn't get base VM and snapshot: %s", err)
	}

	resourcePool, err := i.resourcePool(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't get resource pool: %s", err)
	}

	relocateSpec := types.VirtualMachineRelocateSpec{
		DiskMoveType: string(types.VirtualMachineRelocateDiskMoveOptionsCreateNewChildDiskBacking),
		Pool:         resourcePool,
	}

	cloneSpec := types.VirtualMachineCloneSpec{
		Location: relocateSpec,
		PowerOn:  false,
		Template: false,
		Snapshot: &snapshotTree.Snapshot,
	}

	name := uuid.NewRandom()

	vmFolder, err := i.vmFolder(ctx)
	if err != nil {
		return nil, err
	}

	task, err := vm.Clone(ctx, vmFolder, name.String(), cloneSpec)
	if err != nil {
		return nil, err
	}

	err = task.Wait(ctx)
	if err != nil {
		return nil, err
	}

	var mt mo.Task
	err = task.Properties(ctx, task.Reference(), []string{"info"}, &mt)
	if err != nil {
		return nil, err
	}

	if mt.Info.Result == nil {
		return nil, fmt.Errorf("expected VM, but got nil")
	}

	vmManagedRef, ok := mt.Info.Result.(types.ManagedObjectReference)
	if !ok {
		return nil, fmt.Errorf("expected ManagedObjectReference, but got %T", mt.Info.Result)
	}

	newVM := object.NewVirtualMachine(client.Client, vmManagedRef)

	task, err = newVM.PowerOn(ctx)
	if err != nil {
		return nil, err
	}

	err = task.Wait(ctx)
	if err != nil {
		return nil, err
	}

	return i.instanceForVirtualMachine(ctx, newVM)
}

func (i *vSphereInstanceManager) Terminate(ctx context.Context, id string) error {
	client, err := i.client(ctx)
	if err != nil {
		return err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath+id)
	if err != nil {
		return err
	}

	if vmRef == nil {
		return VirtualMachineNotFoundError{Path: i.paths.VMPath, ID: id}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return fmt.Errorf("not a VM")
	}

	task, err := vm.PowerOff(ctx)
	if err != nil {
		return err
	}

	// Ignore error since the VM may already be powered off. vm.Destroy will fail
	// if the VM is still powered on.
	_ = task.Wait(ctx)

	task, err = vm.Destroy(ctx)
	if err != nil {
		return err
	}

	return task.Wait(ctx)
}

func (i *vSphereInstanceManager) client(ctx context.Context) (*govmomi.Client, error) {
	i.vSphereClientMutex.Lock()
	defer i.vSphereClientMutex.Unlock()

	if i.vSphereClient == nil {
		client, err := i.createClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("couldn't create vSphere client: %s", err)
		}

		i.vSphereClient = client
		return i.vSphereClient, nil
	}

	active, err := i.vSphereClient.SessionManager.SessionIsActive(ctx)
	if err != nil {
		client, err := i.createClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("couldn't create vSphere client: %s", err)
		}

		i.vSphereClient = client
		return i.vSphereClient, nil
	}

	if !active {
		err := i.vSphereClient.SessionManager.Login(ctx, i.vSphereURL.User)
		if err != nil {
			return nil, fmt.Errorf("couldn't log in to vSphere API: %s", err)
		}
	}

	return i.vSphereClient, nil
}

func (i *vSphereInstanceManager) createClient(ctx context.Context) (*govmomi.Client, error) {
	client, err := govmomi.NewClient(ctx, i.vSphereURL, true)
	if err != nil {
		return nil, err
	}

	client.Client.RoundTripper = vim25.Retry(client.Client.RoundTripper, vim25.TemporaryNetworkError(3))

	return client, nil
}

func (i *vSphereInstanceManager) vmFolder(ctx context.Context) (*object.Folder, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	folderRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath)
	if err != nil {
		return nil, err
	}

	if folderRef == nil {
		return nil, fmt.Errorf("VM folder not found")
	}

	folder, ok := folderRef.(*object.Folder)
	if !ok {
		return nil, fmt.Errorf("VM folder is not a folder but %T", folderRef)
	}

	return folder, nil
}

func (i *vSphereInstanceManager) resourcePool(ctx context.Context) (*types.ManagedObjectReference, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	clusterRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.ClusterPath)
	if err != nil {
		return nil, err
	}

	if clusterRef == nil {
		return nil, fmt.Errorf("cluster not found at %s", i.paths.ClusterPath)
	}

	cluster, ok := clusterRef.(*object.ClusterComputeResource)
	if !ok {
		return nil, fmt.Errorf("object at %s is %T, but expected ComputeClusterResource", i.paths.ClusterPath, clusterRef)
	}

	var mccr mo.ClusterComputeResource
	err = cluster.Properties(ctx, cluster.Reference(), []string{"resourcePool"}, &mccr)
	if err != nil {
		return nil, err
	}

	return mccr.ResourcePool, nil
}

func (i *vSphereInstanceManager) findBaseVMAndSnapshot(ctx context.Context, name string) (*object.VirtualMachine, types.VirtualMachineSnapshotTree, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.BasePath+name)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, err
	}

	if vmRef == nil {
		return nil, types.VirtualMachineSnapshotTree{}, BaseVirtualMachineNotFoundError{Path: i.paths.BasePath, Name: name}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, fmt.Errorf("base VM %s is a %T, but expected VirtualMachine", name, vmRef)
	}

	var mvm mo.VirtualMachine
	err = vm.Properties(ctx, vm.Reference(), []string{"snapshot"}, &mvm)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, fmt.Errorf("couldn't get snapshot info for base VM: %s", err)
	}

	if mvm.Snapshot == nil {
		return nil, types.VirtualMachineSnapshotTree{}, fmt.Errorf("invalid base VM (no snapshot)")
	}

	snapshotTree, ok := i.findSnapshot(mvm.Snapshot.RootSnapshotList, "base")
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, fmt.Errorf("invalid base VM (no snapshot named 'base')")
	}

	return vm, snapshotTree, nil
}

func (i *vSphereInstanceManager) findSnapshot(roots []types.VirtualMachineSnapshotTree, name string) (types.VirtualMachineSnapshotTree, bool) {
	for _, snapshotTree := range roots {
		if snapshotTree.Name == name {
			return snapshotTree, true
		}

		tree, ok := i.findSnapshot(snapshotTree.ChildSnapshotList, name)
		if ok {
			return tree, true
		}
	}

	return types.VirtualMachineSnapshotTree{}, false
}

func (i *vSphereInstanceManager) instanceForVirtualMachine(ctx context.Context, vm *object.VirtualMachine) (*Instance, error) {
	var mvm mo.VirtualMachine
	err := vm.Properties(ctx, vm.Reference(), []string{"config", "guest", "runtime"}, &mvm)
	if err != nil {
		return nil, err
	}

	var ipAddresses []string
	for _, nic := range mvm.Guest.Net {
		for _, ip := range nic.IpConfig.IpAddress {
			ipAddresses = append(ipAddresses, ip.IpAddress)
		}
	}

	if reflect.DeepEqual(mvm.Runtime, types.VirtualMachineRuntimeInfo{}) {
		return nil, fmt.Errorf("no instance for vm %v", vm)
	}

	return &Instance{
		ID:          mvm.Config.Name,
		IPAddresses: ipAddresses,
		State:       string(mvm.Runtime.PowerState),
	}, nil
}
