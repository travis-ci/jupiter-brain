// Package server contains an HTTP API server to interface with the Jupiter
// Brain API.
package server

import "log"

// Main sets up and runs the API server with the given configuration.
func Main(cfg *Config) {
	srv, err := newServer(cfg)
	if err != nil {
		log.Fatalf("could not create server: %s", err)
	}

	srv.Setup()
	srv.Run()
}
