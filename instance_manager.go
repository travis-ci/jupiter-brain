package jupiterbrain

import "golang.org/x/net/context"

type InstanceManager interface {
	Fetch(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Start(context.Context, string) (*Instance, error)
	Terminate(context.Context, string) error
}
