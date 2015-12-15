package jupiterbrain

import "fmt"

// VSphereAPIError is returned when a function that communicates with the
// vSphere API gets an error from the API that it can't recover from.
type VSphereAPIError struct {
	// UnderlyingError is the error that was returned by the vSphere API.
	UnderlyingError error
}

func (e VSphereAPIError) Error() string {
	return fmt.Sprintf("vSphere API returned error: %s", e.UnderlyingError)
}

// VirtualMachineNotFoundError is returned if a virtual machine isn't found.
type VirtualMachineNotFoundError struct {
	Path string
	ID   string
}

func (e VirtualMachineNotFoundError) Error() string {
	return fmt.Sprintf("could not find VM with id %s at path %s", e.ID, e.Path)
}

// BaseVirtualMachineNotFoundError is returned if a base virtual machine isn't
// found.
type BaseVirtualMachineNotFoundError struct {
	Path string
	Name string
}

func (e BaseVirtualMachineNotFoundError) Error() string {
	return fmt.Sprintf("could not find base VM with name %s at path %s", e.Name, e.Path)
}
