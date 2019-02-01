package vsphereutil

import (
	"context"
	"net/url"
	"sync"

	"github.com/pkg/errors"
	"github.com/sony/gobreaker"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

// ClientProvider will give you a vSphere client that is reauthenticated as
// needed.
type ClientProvider interface {
	// Get returns a valid vSphere client, creating or reauthenticating it if
	// necessary. An error is returned if the client can't be created or if
	// logging in fails.
	Get(context.Context) (*govmomi.Client, error)
}

// NewClientProvider creates a new client provider that will connect to the
// given URL.
func NewClientProvider(url *url.URL, insecure bool) ClientProvider {
	return &defaultClientProvider{
		url:      url,
		insecure: insecure,
	}
}

type defaultClientProvider struct {
	url      *url.URL
	insecure bool
	client   *govmomi.Client
	mutex    sync.Mutex
}

func (b *defaultClientProvider) Get(ctx context.Context) (*govmomi.Client, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.client == nil {
		client, err := b.createClient(ctx)
		if err != nil {
			return nil, err
		}

		b.client = client
		return b.client, nil
	}

	active, err := b.client.SessionManager.SessionIsActive(ctx)
	if err != nil {
		client, err := b.createClient(ctx)
		if err != nil {
			return nil, err
		}

		b.client = client
		return b.client, nil
	}

	if !active {
		if err := b.client.SessionManager.Login(ctx, b.url.User); err != nil {
			return nil, errors.Wrap(err, "failed to log in to vsphere api")
		}
	}

	return b.client, nil
}

func (b *defaultClientProvider) createClient(ctx context.Context) (*govmomi.Client, error) {
	client, err := govmomi.NewClient(ctx, b.url, b.insecure)
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
