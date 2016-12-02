package main

import (
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	raven "github.com/getsentry/raven-go"
	librato "github.com/mihasya/go-metrics-librato"
	metrics "github.com/rcrowley/go-metrics"
	travismetrics "github.com/travis-ci/jupiter-brain/metrics"
	"github.com/travis-ci/jupiter-brain/server"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Jupiter Brain API server"
	app.Author = "Travis CI"
	app.Email = "contact+jupiter-brain@travis-ci.org"
	app.Version = VersionString
	app.Compiled = GeneratedTime()
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "addr",
			Usage: "host:port to listen to",
			Value: func() string {
				v := ":" + os.Getenv("PORT")
				if v == ":" {
					v = ":42161"
				}
				return v
			}(),
			EnvVar: "JUPITER_BRAIN_ADDR",
		},
		cli.StringFlag{
			Name:   "auth-token",
			Usage:  "authentication token for the api server",
			EnvVar: "JUPITER_BRAIN_AUTH_TOKEN",
		},
		cli.StringFlag{
			Name:   "vsphere-api-url",
			Usage:  "URL to vSphere API",
			EnvVar: "JUPITER_BRAIN_VSPHERE_API_URL,VSPHERE_API_URL",
		},
		cli.StringFlag{
			Name:   "vsphere-base-path",
			Usage:  "path to folder of base VMs in vSphere inventory",
			EnvVar: "JUPITER_BRAIN_VSPHERE_BASE_PATH",
		},
		cli.StringFlag{
			Name:   "vsphere-vm-path",
			Usage:  "path to folder where VMs will be put in vSphere inventory",
			EnvVar: "JUPITER_BRAIN_VSPHERE_VM_PATH",
		},
		cli.StringFlag{
			Name:   "vsphere-cluster-path",
			Usage:  "path to compute cluster that VMs will be booted in",
			EnvVar: "JUPITER_BRAIN_VSPHERE_CLUSTER_PATH",
		},
		cli.IntFlag{
			Name:   "vsphere-concurrent-read-operations",
			Usage:  "number of concurrent fetch and list operations",
			EnvVar: "JUPITER_BRAIN_VSPHERE_CONCURRENT_READ_OPERATIONS",
			Value:  4,
		},
		cli.IntFlag{
			Name:   "vsphere-concurrent-create-operations",
			Usage:  "number of concurrent start operations",
			EnvVar: "JUPITER_BRAIN_VSPHERE_CONCURRENT_CREATE_OPERATIONS",
			Value:  48,
		},
		cli.IntFlag{
			Name:   "vsphere-concurrent-delete-operations",
			Usage:  "number of concurrent terminate operations",
			EnvVar: "JUPITER_BRAIN_VSPHERE_CONCURRENT_DELETE_OPERATIONS",
			Value:  48,
		},
		cli.StringFlag{
			Name:   "database-url",
			Usage:  "URL to the PostgreSQL database",
			EnvVar: "JUPITER_BRAIN_DATABASE_URL,DATABASE_URL",
		},
		cli.BoolFlag{
			Name:   "debug",
			Usage:  "enable debug logging",
			EnvVar: "JUPITER_BRAIN_DEBUG,DEBUG",
		},
		cli.StringFlag{
			Name:   "sentry-dsn",
			Usage:  "Sentry DSN to send errors to",
			EnvVar: "JUPITER_BRAIN_SENTRY_DSN,SENTRY_DSN",
		},
		cli.StringFlag{
			Name:   "librato-email",
			Usage:  "Email for Librato account to send metrics to",
			EnvVar: "JUPITER_BRAIN_LIBRATO_EMAIL,LIBRATO_EMAIL",
		},
		cli.StringFlag{
			Name:   "librato-token",
			Usage:  "Token for Librato account to send metrics to",
			EnvVar: "JUPITER_BRAIN_LIBRATO_TOKEN,LIBRATO_TOKEN",
		},
		cli.StringFlag{
			Name:   "librato-source",
			Usage:  "The source to use when sending metrics to Librato",
			EnvVar: "JUPITER_BRAIN_LIBRATO_SOURCE,LIBRATO_SOURCE",
		},
		cli.StringFlag{
			Name:   "pprof-addr",
			Usage:  "Whether to enable pprof endpoints over HTTP",
			EnvVar: "JUPITER_BRAIN_PPROF_ADDR",
		},
		cli.DurationFlag{
			Name:   "request-timeout",
			Usage:  "The max time an incoming HTTP request can take before timing out",
			EnvVar: "JUPITER_BRAIN_REQUEST_TIMEOUT",
			Value:  3 * time.Minute,
		},
	}
	app.Action = runServer

	app.RunAndExitOnError()
}

func runServer(c *cli.Context) {
	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	if c.String("librato-email") != "" && c.String("librato-token") != "" && c.String("librato-source") != "" {
		logrus.Info("starting librato metrics reporter")

		go librato.Librato(
			metrics.DefaultRegistry,
			time.Minute,
			c.String("librato-email"),
			c.String("librato-token"),
			c.String("librato-source"),
			[]float64{0.50, 0.75, 0.90, 0.95, 0.99, 0.999, 1.0},
			time.Millisecond,
		)
	}
	go travismetrics.ReportMemstatsMetrics()

	raven.SetDSN(c.String("sentry-dsn"))
	raven.SetRelease(VersionString)

	server.Main(&server.Config{
		Addr:      c.String("addr"),
		AuthToken: c.String("auth-token"),
		Debug:     c.Bool("debug"),
		SentryDSN: c.String("sentry-dsn"),

		VSphereURL:                        c.String("vsphere-api-url"),
		VSphereBasePath:                   c.String("vsphere-base-path"),
		VSphereVMPath:                     c.String("vsphere-vm-path"),
		VSphereClusterPath:                c.String("vsphere-cluster-path"),
		VSphereConcurrentReadOperations:   c.Int("vsphere-concurrent-read-operations"),
		VSphereConcurrentCreateOperations: c.Int("vsphere-concurrent-create-operations"),
		VSphereConcurrentDeleteOperations: c.Int("vsphere-concurrent-delete-operations"),

		DatabaseURL: c.String("database-url"),

		PprofAddr:      c.String("pprof-addr"),
		RequestTimeout: c.Duration("request-timeout"),
	})
}
