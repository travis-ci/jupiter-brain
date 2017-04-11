package server

import "time"

// Config contains everything needed to run the API server.
type Config struct {
	// Addr is the address the API should listen on.
	Addr string

	// AuthToken is the token that should be passed in the Authorization
	// header for all requests to the API.
	AuthToken string

	// Debug determines whether debug log messages should be printed or not.
	Debug bool

	// VSphereURL is the URL to the vSphere SDK. Normally this is on the
	// form https://user:password@address:443/sdk
	VSphereURL string

	// The paths are documented in the jupiterbrain.VSpherePaths struct.
	VSphereBasePath    string
	VSphereVMPath      string
	VSphereClusterPath string

	// The VSphereConcurrent*Operations attributes specify the number of
	// concurrent read, create and delete operations Jupiter Brain has with
	// vSphere at any given time.
	VSphereConcurrentReadOperations   int
	VSphereConcurrentCreateOperations int
	VSphereConcurrentDeleteOperations int

	// SentryDSN is used to send errors to Sentry. Leave this blank to not
	// send errors.
	SentryDSN string

	// SentryEnvironment is the environment string to use when sending errors
	// to Sentry. Has no effect if SentryDSN is empty.
	SentryEnvironment string

	// DatabaseURL is the PostgreSQL database URL wow!
	DatabaseURL string

	// DatabasePoolSize is the Database Connection pool size wow!
	DatabasePoolSize int

	// PprofAddr should be a non-empty string specifying where to bind
	// net/http/pprof endpoints
	PprofAddr string

	// RequestTimeout is the maximum amount of time a request is allowed to
	// take
	RequestTimeout time.Duration
}
