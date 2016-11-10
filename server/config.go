package server

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

	// DatabaseURL is the PostgreSQL database URL wow!
	DatabaseURL string

	// EnablePprof toggles whether the pprof endpoints from net/http/pprof
	// should be enabled or not
	EnablePprof bool
}
