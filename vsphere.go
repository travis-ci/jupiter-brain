package jupiterbrain

import (
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/sony/gobreaker"
	"github.com/travis-ci/jupiter-brain/metrics"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

type vSphereInstanceManager struct {
	log *logrus.Logger

	concurrencySem chan struct{}

	vSphereClientMutex sync.Mutex
	vSphereClient      *govmomi.Client

	vSphereURL *url.URL

	paths VSpherePaths
}

type soapBreakerRoundTripper struct {
	rt soap.RoundTripper
	cb *gobreaker.CircuitBreaker
}

func (rt *soapBreakerRoundTripper) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	_, err := rt.cb.Execute(func() (interface{}, error) {
		err := rt.rt.RoundTrip(ctx, req, res)
		return nil, err
	})

	return err
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
func NewVSphereInstanceManager(log *logrus.Logger, vSphereURL *url.URL, paths VSpherePaths, concurrency int) InstanceManager {
	return &vSphereInstanceManager{
		log:            log,
		vSphereURL:     vSphereURL,
		paths:          paths,
		concurrencySem: make(chan struct{}, concurrency),
	}
}

func (i *vSphereInstanceManager) Fetch(ctx context.Context, id string) (*Instance, error) {
	select {
	case i.concurrencySem <- struct{}{}:
		defer func() { <-i.concurrencySem }()
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "timed out waiting for conurrency semaphore")
	}

	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath+id)
	if err != nil {
		return nil, errors.Wrap(err, "vm search failed")
	}

	if vmRef == nil {
		return nil, VirtualMachineNotFoundError{Path: i.paths.VMPath, ID: id}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return nil, errors.Errorf("object at path %s is a %T, but expected VirtualMachine", i.paths.VMPath+id, vmRef)
	}

	return i.instanceForVirtualMachine(ctx, vm)
}

func (i *vSphereInstanceManager) List(ctx context.Context) ([]*Instance, error) {
	select {
	case i.concurrencySem <- struct{}{}:
		defer func() { <-i.concurrencySem }()
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "timed out waiting for conurrency semaphore")
	}

	folder, err := i.vmFolder(ctx)
	if err != nil {
		return nil, err
	}

	vms, err := folder.Children(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list children of vm folder")
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
	select {
	case i.concurrencySem <- struct{}{}:
		defer func() { <-i.concurrencySem }()
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "timed out waiting for conurrency semaphore")
	}

	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	vm, snapshotTree, err := i.findBaseVMAndSnapshot(ctx, baseName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find base VM and snapshot")
	}

	resourcePool, err := i.resourcePool(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find resource pool")
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

	cloneStartTime := time.Now()
	task, err := vm.Clone(ctx, vmFolder, name.String(), cloneSpec)
	if err != nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "failed to create vm clone task")
	}

	err = task.Wait(ctx)
	if err != nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "vm clone task failed")
	}
	metrics.TimeSince("travis.jupiter-brain.tasks.clone", cloneStartTime)

	var mt mo.Task
	err = task.Properties(ctx, task.Reference(), []string{"info"}, &mt)
	if err != nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "failed to get vm info properties")
	}

	if mt.Info.Result == nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Errorf("expected VM, but got nil")
	}

	vmManagedRef, ok := mt.Info.Result.(types.ManagedObjectReference)
	if !ok {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Errorf("expected ManagedObjectReference, but got %T", mt.Info.Result)
	}

	newVM := object.NewVirtualMachine(client.Client, vmManagedRef)

	powerOnStartTime := time.Now()
	task, err = newVM.PowerOn(ctx)
	if err != nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "failed to create vm power on task")
	}

	err = task.Wait(ctx)
	if err != nil {
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "vm power on task failed")
	}
	metrics.TimeSince("travis.jupiter-brain.tasks.power-on", powerOnStartTime)

	return i.instanceForVirtualMachine(ctx, newVM)
}

func (i *vSphereInstanceManager) Terminate(ctx context.Context, id string) error {
	select {
	case i.concurrencySem <- struct{}{}:
		defer func() { <-i.concurrencySem }()
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "timed out waiting for conurrency semaphore")
	}

	client, err := i.client(ctx)
	if err != nil {
		return err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath+id)
	if err != nil {
		return errors.Wrap(err, "failed to search for vm")
	}

	if vmRef == nil {
		return VirtualMachineNotFoundError{Path: i.paths.VMPath, ID: id}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return errors.New("not a VM")
	}

	task, err := vm.PowerOff(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create vm power off task")
	}

	// Ignore error since the VM may already be powered off. vm.Destroy will fail
	// if the VM is still powered on.
	_ = task.Wait(ctx)

	task, err = vm.Destroy(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create vm destroy task")
	}

	err = task.Wait(ctx)
	return errors.Wrap(err, "vm destroy task failed")
}

// Terminate the VM if it exists
func (i *vSphereInstanceManager) terminateIfExists(ctx context.Context, name string) {
	_, err := i.Fetch(ctx, name)
	if _, ok := err.(VirtualMachineNotFoundError); ok {
		return
	}

	i.Terminate(ctx, name)
}

func (i *vSphereInstanceManager) client(ctx context.Context) (*govmomi.Client, error) {
	i.vSphereClientMutex.Lock()
	defer i.vSphereClientMutex.Unlock()

	if i.vSphereClient == nil {
		client, err := i.createClient(ctx)
		if err != nil {
			return nil, err
		}

		i.vSphereClient = client
		return i.vSphereClient, nil
	}

	active, err := i.vSphereClient.SessionManager.SessionIsActive(ctx)
	if err != nil {
		client, err := i.createClient(ctx)
		if err != nil {
			return nil, err
		}

		i.vSphereClient = client
		return i.vSphereClient, nil
	}

	if !active {
		err := i.vSphereClient.SessionManager.Login(ctx, i.vSphereURL.User)
		if err != nil {
			return nil, errors.Wrap(err, "failed to log in to vsphere api")
		}
	}

	return i.vSphereClient, nil
}

func (i *vSphereInstanceManager) createClient(ctx context.Context) (*govmomi.Client, error) {
	client, err := govmomi.NewClient(ctx, i.vSphereURL, true)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create govmomi client")
	}

	client.Client.RoundTripper = vim25.Retry(client.Client.RoundTripper, vim25.TemporaryNetworkError(3))
	client.Client.RoundTripper = &soapBreakerRoundTripper{
		rt: client.Client.RoundTripper,
		cb: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name: "vSphere govmomi",
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 3 && failureRatio >= 0.6
			},
		}),
	}

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
		return nil, errors.Wrap(err, "failed to search for vm folder")
	}

	if folderRef == nil {
		return nil, errors.New("VM folder not found")
	}

	folder, ok := folderRef.(*object.Folder)
	if !ok {
		return nil, errors.Errorf("VM folder is not a folder but %T", folderRef)
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
		return nil, errors.Wrap(err, "cluster search failed")
	}

	if clusterRef == nil {
		return nil, errors.Errorf("cluster not found at %s", i.paths.ClusterPath)
	}

	cluster, ok := clusterRef.(*object.ClusterComputeResource)
	if !ok {
		return nil, errors.Errorf("object at %s is %T, but expected ComputeClusterResource", i.paths.ClusterPath, clusterRef)
	}

	var mccr mo.ClusterComputeResource
	err = cluster.Properties(ctx, cluster.Reference(), []string{"resourcePool"}, &mccr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find cluster resourcePool property")
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
		return nil, types.VirtualMachineSnapshotTree{}, errors.Wrap(err, "failed to search for VM")
	}

	if vmRef == nil {
		return nil, types.VirtualMachineSnapshotTree{}, BaseVirtualMachineNotFoundError{Path: i.paths.BasePath, Name: name}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, errors.Errorf("base VM %s is a %T, but expected VirtualMachine", name, vmRef)
	}

	var mvm mo.VirtualMachine
	err = vm.Properties(ctx, vm.Reference(), []string{"snapshot"}, &mvm)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, errors.Wrap(err, "failed to get snapshot")
	}

	if mvm.Snapshot == nil {
		return nil, types.VirtualMachineSnapshotTree{}, errors.Errorf("no snapshots")
	}

	snapshotTree, ok := i.findSnapshot(mvm.Snapshot.RootSnapshotList, "base")
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, errors.Errorf("no snapshot with name 'base'")
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

func (i *vSphereInstanceManager) instanceForVirtualMachine(ctx context.Context, vm *object.VirtualMachine) (inst *Instance, err error) {
	defer func() {
		recoverErr := recover()
		if recoverErr != nil {
			inst = nil
			err = recoverErr.(error)
		}
	}()

	var mvm mo.VirtualMachine
	err = vm.Properties(ctx, vm.Reference(), []string{"config", "guest", "runtime"}, &mvm)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get vm properties")
	}

	var ipAddresses []string
	for _, nic := range mvm.Guest.Net {
		for _, ip := range nic.IpConfig.IpAddress {
			ipAddresses = append(ipAddresses, ip.IpAddress)
		}
	}

	if reflect.DeepEqual(mvm.Runtime, types.VirtualMachineRuntimeInfo{}) {
		return nil, errors.Errorf("no instance for vm %v", vm)
	}

	return &Instance{
		ID:          mvm.Config.Name,
		IPAddresses: ipAddresses,
		State:       string(mvm.Runtime.PowerState),
	}, nil
}
