package jupiterbrain

import "golang.org/x/net/context"

// InstanceManager is the interface used for managing instances wow!
type InstanceManager interface {
	Fetch(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Start(context.Context, string) (*Instance, error)
	Terminate(context.Context, string) error
}
