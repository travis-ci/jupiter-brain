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

	// SentryDSN is used to send errors to Sentry. Leave this blank to not
	// send errors.
	SentryDSN string
}
