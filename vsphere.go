package jupiterbrain

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/getsentry/raven-go"
	"github.com/honeycombio/beeline-go"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/travis-ci/jupiter-brain/metrics"
	"github.com/travis-ci/jupiter-brain/pkg/vsphereutil"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type vSphereInstanceManager struct {
	log *logrus.Logger

	readConcurrencySem   chan struct{}
	createConcurrencySem chan struct{}
	deleteConcurrencySem chan struct{}

	clientProvider vsphereutil.ClientProvider

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
func NewVSphereInstanceManager(log *logrus.Logger, vSphereURL *url.URL, paths VSpherePaths, readConcurrency, createConcurrency, deleteConcurrency int) InstanceManager {
	return &vSphereInstanceManager{
		log:                  log,
		clientProvider:       vsphereutil.NewClientProvider(vSphereURL, true),
		paths:                paths,
		readConcurrencySem:   make(chan struct{}, readConcurrency),
		createConcurrencySem: make(chan struct{}, createConcurrency),
		deleteConcurrencySem: make(chan struct{}, deleteConcurrency),
	}
}

func (i *vSphereInstanceManager) Fetch(ctx context.Context, id string) (*Instance, error) {
	releaseSem, err := i.requestReadSemaphore(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "timed out waiting for concurrency semaphore")
	}
	defer releaseSem()

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
	releaseSem, err := i.requestReadSemaphore(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "timed out waiting for concurrency semaphore")
	}
	defer releaseSem()

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

func (i *vSphereInstanceManager) Start(ctx context.Context, config InstanceConfig) (*Instance, error) {
	startTime := time.Now()
	config.Track(ctx)

	releaseSem, err := i.requestCreateSemaphore(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "waiting_for_semaphore")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return nil, errors.Wrap(err, "timed out waiting for concurrency semaphore")
	}
	autoreleaseSem := true
	defer func() {
		if autoreleaseSem {
			releaseSem()
		}
	}()
	beeline.AddField(ctx, "semaphore_ms", float64(time.Since(startTime).Nanoseconds())/1000000.0)

	client, err := i.client(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "get_client")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return nil, err
	}

	vm, snapshotTree, isFrozen, err := i.findBaseVMAndSnapshot(ctx, config.BaseImage)
	if err != nil {
		beeline.AddField(ctx, "stage", "find_base_vm_and_snapshot")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return nil, errors.Wrap(err, "failed to find base VM and snapshot")
	}

	if host, _ := vm.HostSystem(ctx); host != nil {
		var mh mo.HostSystem
		err := host.Properties(ctx, host.Reference(), []string{"name"}, &mh)
		if err == nil {
			beeline.AddField(ctx, "image_host_name", mh.Name)
		}
	}

	name := uuid.NewRandom()
	beeline.AddField(ctx, "vm_name", name)

	vmFolder, err := i.vmFolder(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "find_vm_folder")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return nil, err
	}

	resourcePool, err := i.resourcePool(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find resource pool")
	}

	beeline.AddField(ctx, "setup_ms", float64(time.Since(startTime).Nanoseconds())/1000000.0)

	cloneStartTime := time.Now()

	var task *object.Task
	if isFrozen {
		task, err = i.createInstantClone(ctx, vm, vmFolder, name.String(), resourcePool)
	} else {
		task, err = i.createLinkedClone(ctx, vm, snapshotTree, vmFolder, name.String(), resourcePool)
	}

	if err != nil {
		beeline.AddField(ctx, "stage", "clone_vm_task")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		go i.terminateIfExists(ctx, name.String())
		return nil, errors.Wrap(err, "failed to create vm clone task")
	}

	errChan := make(chan error, 1)
	vmChan := make(chan *object.VirtualMachine, 1)

	autoreleaseSem = false
	go func() {
		defer releaseSem()

		// Set up a different context to prevent the goroutine from being
		// cancelled by the HTTP request finishing, since that would lead to a
		// leaked VMs
		backgroundCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := task.Wait(backgroundCtx)
		if err != nil && isFrozen {
			// If an instant clone fails, fallback to an ordinary linked clone
			isFrozen = false
			task, err = i.createLinkedClone(backgroundCtx, vm, snapshotTree, vmFolder, name.String(), resourcePool)
			if err != nil {
				beeline.AddField(ctx, "stage", "clone_vm_task")
				beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
				go i.terminateIfExists(backgroundCtx, name.String())
				errChan <- errors.Wrap(err, "failed to create vm clone task")
				return
			}

			err = task.Wait(backgroundCtx)
		}

		if err != nil {
			beeline.AddField(ctx, "stage", "clone_vm_task_wait")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			go i.terminateIfExists(backgroundCtx, name.String())
			if err != context.Canceled && err != context.DeadlineExceeded {
				var interfaces []raven.Interface

				faultCause := getFaultCause(err)
				if faultCause != "" {
					interfaces = append(interfaces, &raven.Message{
						Message: fmt.Sprintf("faultCause: %s", faultCause),
					})
				}

				raven.CaptureError(err, map[string]string{"vm-name": name.String(), "task": "clone"}, interfaces...)
			}
			errChan <- errors.Wrap(err, "vm clone task failed")
			return
		}
		metrics.TimeSince("travis.jupiter-brain.tasks.clone", cloneStartTime)
		beeline.AddField(ctx, "clone_ms", float64(time.Since(cloneStartTime).Nanoseconds())/1000000.0)

		var mt mo.Task
		err = task.Properties(backgroundCtx, task.Reference(), []string{"info"}, &mt)
		if err != nil {
			beeline.AddField(ctx, "stage", "vm_clone_get_info")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			go i.terminateIfExists(backgroundCtx, name.String())
			errChan <- errors.Wrap(err, "failed to get vm info properties")
			return
		}

		if mt.Info.Result == nil {
			err = errors.Errorf("expected VM, but got nil")
			beeline.AddField(ctx, "stage", "got_nil_vm")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			go i.terminateIfExists(backgroundCtx, name.String())
			errChan <- err
			return
		}

		vmManagedRef, ok := mt.Info.Result.(types.ManagedObjectReference)
		if !ok {
			err = errors.Errorf("expected ManagedObjectReference, but got %T", mt.Info.Result)
			beeline.AddField(ctx, "stage", "vm_not_a_mo_ref")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			go i.terminateIfExists(backgroundCtx, name.String())
			errChan <- err
			return
		}

		newVM := object.NewVirtualMachine(client.Client, vmManagedRef)

		if host, _ := newVM.HostSystem(backgroundCtx); host != nil {
			var mh mo.HostSystem
			err := host.Properties(backgroundCtx, host.Reference(), []string{"name"}, &mh)
			if err == nil {
				beeline.AddField(ctx, "vm_clone_host_name", mh.Name)
			}
		}

		if ctx.Err() != nil {
			beeline.AddField(ctx, "stage", "abandoning_after_clone")
			beeline.AddField(ctx, "err", ctx.Err().Error())
			// The HTTP context is cancelled, so let's delete the VM we just cloned instead of powering it on
			errChan <- errors.Wrap(i.Terminate(backgroundCtx, name.String()), "error while trying to delete abandoned VM")
			return
		}

		// If we do an instant clone, the VM can't be reconfigured and will already be running.
		if !isFrozen {
			// Reconfigure the VM if any changes from the base VM properties were requested.
			configSpec := config.ConfigSpec()
			if configSpec != nil {
				task, err = newVM.Reconfigure(backgroundCtx, *configSpec)
				if err != nil {
					beeline.AddField(ctx, "stage", "reconfigure_vm_task")
					beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
					go i.terminateIfExists(backgroundCtx, name.String())
					errChan <- errors.Wrap(err, "failed to create reconfigure vm task")
					return
				}

				err = task.Wait(backgroundCtx)
				if err != nil {
					beeline.AddField(ctx, "stage", "reconfigure_vm_task_wait")
					beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
					go i.terminateIfExists(backgroundCtx, name.String())
					errChan <- errors.Wrap(err, "reconfigure vm task failed")
					return
				}
			}

			powerOnStartTime := time.Now()
			task, err = newVM.PowerOn(backgroundCtx)
			if err != nil {
				beeline.AddField(ctx, "stage", "power_on_vm_task")
				beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
				go i.terminateIfExists(backgroundCtx, name.String())
				errChan <- errors.Wrap(err, "failed to create vm power on task")
				return
			}

			err = task.Wait(backgroundCtx)
			if err != nil {
				beeline.AddField(ctx, "stage", "power_on_vm_task_wait")
				beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
				go i.terminateIfExists(backgroundCtx, name.String())
				if err != context.Canceled && err != context.DeadlineExceeded {
					var interfaces []raven.Interface

					faultCause := getFaultCause(err)
					if faultCause != "" {
						interfaces = append(interfaces, &raven.Message{
							Message: fmt.Sprintf("faultCause: %s", faultCause),
						})
					}

					raven.CaptureError(err, map[string]string{"vm-name": name.String(), "task": "power-on"}, interfaces...)
				}
				errChan <- errors.Wrap(err, "vm power on task failed")
				return
			}
			metrics.TimeSince("travis.jupiter-brain.tasks.power-on", powerOnStartTime)
			metrics.TimeSince("travis.jupiter-brain.tasks.full-start", startTime)
			beeline.AddField(ctx, "power_on_ms", float64(time.Since(powerOnStartTime).Nanoseconds())/1000000.0)
		}

		if host, _ := newVM.HostSystem(ctx); host != nil {
			var mh mo.HostSystem
			err := host.Properties(ctx, host.Reference(), []string{"name"}, &mh)
			if err == nil {
				beeline.AddField(ctx, "vm_power_on_host_name", mh.Name)
			}
		}

		if ctx.Err() != nil {
			beeline.AddField(ctx, "stage", "abandoning_after_power_on")
			beeline.AddField(ctx, "err", ctx.Err().Error())
			// The HTTP context is cancelled, so let's delete the VM we just cloned instead of returning it
			errChan <- errors.Wrap(i.Terminate(backgroundCtx, name.String()), "error while trying to delete abandoned VM")
			return
		}

		beeline.AddField(ctx, "stage", "finished")
		vmChan <- newVM
	}()

	select {
	case vm := <-vmChan:
		return i.instanceForVirtualMachine(ctx, vm)
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		return nil, errors.Wrap(ctx.Err(), "context finished while waiting for VM clone and power on")
	}
}

func (i *vSphereInstanceManager) createLinkedClone(ctx context.Context, baseVM *object.VirtualMachine, snapshotTree types.VirtualMachineSnapshotTree, vmFolder *object.Folder, name string, pool *types.ManagedObjectReference) (*object.Task, error) {
	beeline.AddField(ctx, "instant_clone", false)

	relocateSpec := types.VirtualMachineRelocateSpec{
		DiskMoveType: string(types.VirtualMachineRelocateDiskMoveOptionsCreateNewChildDiskBacking),
		Pool:         pool,
	}

	cloneSpec := types.VirtualMachineCloneSpec{
		Location: relocateSpec,
		PowerOn:  false,
		Template: false,
		Snapshot: &snapshotTree.Snapshot,
	}

	return baseVM.Clone(ctx, vmFolder, name, cloneSpec)
}

func (i *vSphereInstanceManager) createInstantClone(ctx context.Context, baseVM *object.VirtualMachine, vmFolder *object.Folder, name string, pool *types.ManagedObjectReference) (*object.Task, error) {
	beeline.AddField(ctx, "instant_clone", true)

	destFolderRef := vmFolder.Reference()
	cloneSpec := types.VirtualMachineInstantCloneSpec{
		Name: name,
		Location: types.VirtualMachineRelocateSpec{
			Folder: &destFolderRef,
			Pool:   pool,
		},
	}

	req := types.InstantClone_Task{
		This: baseVM.Reference(),
		Spec: cloneSpec,
	}

	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	res, err := methods.InstantClone_Task(ctx, client.Client, &req)
	if err != nil {
		return nil, err
	}

	return object.NewTask(client.Client, res.Returnval), nil
}

func (i *vSphereInstanceManager) Terminate(ctx context.Context, id string) error {
	startTime := time.Now()
	beeline.AddField(ctx, "vm_name", id)

	releaseSem, err := i.requestDeleteSemaphore(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "waiting_for_semaphore")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return errors.Wrap(err, "timed out waiting for concurrency semaphore")
	}
	autoreleaseSem := true
	defer func() {
		if autoreleaseSem {
			releaseSem()
		}
	}()
	beeline.AddField(ctx, "semaphore_ms", float64(time.Since(startTime).Nanoseconds())/1000000.0)

	client, err := i.client(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "get_client")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.VMPath+id)
	if err != nil {
		beeline.AddField(ctx, "stage", "find_vm")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return errors.Wrap(err, "failed to search for vm")
	}

	if vmRef == nil {
		err = VirtualMachineNotFoundError{Path: i.paths.VMPath, ID: id}
		beeline.AddField(ctx, "stage", "vm_404")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return err
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		err = errors.Errorf("not a VM, but a %T", vm)
		beeline.AddField(ctx, "stage", "vm_incorrect_type")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return err
	}

	powerOffStartTime := time.Now()
	task, err := vm.PowerOff(ctx)
	if err != nil {
		beeline.AddField(ctx, "stage", "vm_power_off_task")
		beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
		return errors.Wrap(err, "failed to create vm power off task")
	}

	errChan := make(chan error, 1)

	autoreleaseSem = false
	go func() {
		defer releaseSem()

		// Set up a different context to prevent the goroutine from being
		// cancelled by the HTTP request finishing, since that would lead to a
		// leaked VMs
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = task.Wait(ctx)
		if err != nil {
			// Ignore error since the VM may already be powered off. vm.Destroy will fail
			// if the VM is still powered on.

			// Send the error to Honeycomb, though
			beeline.AddField(ctx, "power_off_err", fmt.Sprintf("%s", err))
		}
		beeline.AddField(ctx, "power_off_ms", float64(time.Since(powerOffStartTime).Nanoseconds())/1000000.0)

		destroyStartTime := time.Now()
		task, err = vm.Destroy(ctx)
		if err != nil {
			beeline.AddField(ctx, "stage", "destroy_vm_task")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			errChan <- errors.Wrap(err, "failed to create vm destroy task")
			return
		}

		err = task.Wait(ctx)
		if err != nil {
			beeline.AddField(ctx, "stage", "destroy_vm_task_wait")
			beeline.AddField(ctx, "err", fmt.Sprintf("%s", err))
			errChan <- errors.Wrap(err, "vm destroy task failed")
			return
		}
		beeline.AddField(ctx, "destroy_ms", float64(time.Since(destroyStartTime).Nanoseconds())/1000000.0)

		beeline.AddField(ctx, "stage", "finished")
		errChan <- nil
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "context finished while waiting for VM power off and destroy")
	}
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
	return i.clientProvider.Get(ctx)
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

func (i *vSphereInstanceManager) findBaseVMAndSnapshot(ctx context.Context, name string) (*object.VirtualMachine, types.VirtualMachineSnapshotTree, bool, error) {
	client, err := i.client(ctx)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, err
	}

	searchIndex := object.NewSearchIndex(client.Client)

	vmRef, err := searchIndex.FindByInventoryPath(ctx, i.paths.BasePath+name)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Wrap(err, "failed to search for VM")
	}

	if vmRef == nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, BaseVirtualMachineNotFoundError{Path: i.paths.BasePath, Name: name}
	}

	vm, ok := vmRef.(*object.VirtualMachine)
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Errorf("base VM %s is a %T, but expected VirtualMachine", name, vmRef)
	}

	var mvm mo.VirtualMachine
	err = vm.Properties(ctx, vm.Reference(), []string{"snapshot", "runtime.instantCloneFrozen"}, &mvm)
	if err != nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Wrap(err, "failed to get snapshot")
	}

	if mvm.Snapshot == nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Errorf("no snapshots")
	}

	snapshotTree, ok := i.findSnapshot(mvm.Snapshot.RootSnapshotList, "base")
	if !ok {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Errorf("no snapshot with name 'base'")
	}

	if mvm.Runtime.InstantCloneFrozen == nil {
		return nil, types.VirtualMachineSnapshotTree{}, false, errors.Errorf("could not determine frozen status")
	}

	return vm, snapshotTree, *mvm.Runtime.InstantCloneFrozen, nil
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

func (i *vSphereInstanceManager) requestReadSemaphore(ctx context.Context) (func(), error) {
	metrics.Gauge("travis.jupiter-brain.semaphore.read.waiting", int64(len(i.readConcurrencySem)))
	defer metrics.TimeSince("travis.jupiter-brain.semaphore.read.wait-time", time.Now())

	return i.requestSemaphore(ctx, i.readConcurrencySem)
}

func (i *vSphereInstanceManager) requestCreateSemaphore(ctx context.Context) (func(), error) {
	metrics.Gauge("travis.jupiter-brain.semaphore.create.waiting", int64(len(i.createConcurrencySem)))
	defer metrics.TimeSince("travis.jupiter-brain.semaphore.create.wait-time", time.Now())

	return i.requestSemaphore(ctx, i.createConcurrencySem)
}

func (i *vSphereInstanceManager) requestDeleteSemaphore(ctx context.Context) (func(), error) {
	metrics.Gauge("travis.jupiter-brain.semaphore.delete.waiting", int64(len(i.deleteConcurrencySem)))
	defer metrics.TimeSince("travis.jupiter-brain.semaphore.delete.wait-time", time.Now())

	return i.requestSemaphore(ctx, i.deleteConcurrencySem)
}

func (i *vSphereInstanceManager) requestSemaphore(ctx context.Context, semaphore chan struct{}) (func(), error) {
	select {
	case semaphore <- struct{}{}:
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func getFaultCause(err error) string {
	err = errors.Cause(err)

	faultErr, ok := err.(types.HasFault)
	if !ok {
		return ""
	}

	fault := faultErr.Fault()
	if fault == nil {
		return ""
	}

	methodFault := fault.GetMethodFault()
	if methodFault == nil {
		return ""
	}

	faultCause := methodFault.FaultCause
	if faultCause == nil {
		return ""
	}

	return faultCause.LocalizedMessage
}
