package jupiterbrain

import (
	"time"

	"github.com/lib/pq"
	"github.com/vmware/govmomi/vim25/types"
)

// Instance is our representation of an instance woop woop
type Instance struct {
	ID          string `db:"id"`
	IPAddresses []string
	State       string
	CreatedAt   time.Time   `db:"created_at"`
	DestroyedAt pq.NullTime `db:"destroyed_at"`
}

// InstanceConfig specifies how a new instance should be configured
type InstanceConfig struct {
	// Type is the type of data in the payload and is required to be "instances".
	// The endpoint for creating new instances will check this value for correctness.
	Type string `json:"type"`

	// BaseImage is the VM name of the image the new instance will be cloned from.
	BaseImage string `json:"base-image"`

	// CPUCount is the number of CPUs the new instance should have.
	// If CPUCount is 0, the number of CPUs will not be changed from what is configured
	// in the base image.
	CPUCount int `json:"cpus"`

	// RAM is the amount of RAM in MB that the new instance should have.
	// If RAM is 0, the amount of RAM will not be changed from what is configured in the
	// base image.
	RAM int `json:"ram"`
}

// ConfigSpec constructs a spec for reconfiguring the VM after it is cloned.
// ConfigSpec returns nil if none of the config parameters for the VM are set.
// In that case, no reconfigure task for the VM should be run.
func (c InstanceConfig) ConfigSpec() *types.VirtualMachineConfigSpec {
	if c.CPUCount == 0 && c.RAM == 0 {
		return nil
	}

	return &types.VirtualMachineConfigSpec{
		NumCPUs:  int32(c.CPUCount),
		MemoryMB: int64(c.RAM),
	}
}
