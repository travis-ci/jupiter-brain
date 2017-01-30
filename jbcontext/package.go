package jbcontext

type contextKey struct {
	name string
}

func (k *contextKey) String() string { return "jupiter-brain context value " + k.name }

var (
	RequestIDKey = &contextKey{"request-id"}
)
