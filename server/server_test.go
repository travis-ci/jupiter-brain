package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/travis-ci/jupiter-brain"
	"golang.org/x/net/context"
)

type fakeInstanceManager struct{}

func (i fakeInstanceManager) Fetch(ctx context.Context, id string) (*jupiterbrain.Instance, error) {
	return nil, nil
}

func (i fakeInstanceManager) List(ctx context.Context) ([]*jupiterbrain.Instance, error) {
	return []*jupiterbrain.Instance{}, nil
}

func (i fakeInstanceManager) Start(ctx context.Context, image string) (*jupiterbrain.Instance, error) {
	return nil, nil
}

func (i fakeInstanceManager) Terminate(ctx context.Context, id string) error {
	return nil
}

func TestAuth(t *testing.T) {
	srv, err := newServer(&Config{
		AuthToken:  "hello-world",
		VSphereURL: "http://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv.i = fakeInstanceManager{}

	srv.Setup()
	ts := httptest.NewServer(srv.n)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/instances", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status code %d but got %d", http.StatusUnauthorized, resp.StatusCode)
	}

	req.Header.Set("Authorization", "token hello-world")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d but got %d", http.StatusOK, resp.StatusCode)
	}

	req.Header.Set("Authorization", "token hello-world-incorrect")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status code %d but got %d", http.StatusUnauthorized, resp.StatusCode)
	}
}
