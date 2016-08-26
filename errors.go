package jupiterbrain

import "fmt"

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
