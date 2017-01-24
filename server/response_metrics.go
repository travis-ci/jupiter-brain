package server

import (
	"fmt"
	"net/http"
	"time"

	libhoney "github.com/honeycombio/libhoney-go"
	"github.com/travis-ci/jupiter-brain/metrics"
)

var (
	requestHoneycombEventBuilder = libhoney.NewBuilder()
)

func init() {
	requestHoneycombEventBuilder.Dataset = "jupiter-brain-requests"
}

type metricsResponseWriter struct {
	http.ResponseWriter

	req   *http.Request
	start time.Time
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	metrics.Mark(fmt.Sprintf("travis.jupiter-brain.response-code.%d", code))

	requestHoneycombEventBuilder.SendNow(map[string]interface{}{
		"event":         "finished",
		"duration_ms":   float64(mrw.start.Sub(time.Now()).Nanoseconds()) / 1000000.0,
		"method":        mrw.req.Method,
		"endpoint":      mrw.req.URL.Path,
		"response_code": code,
	})

	mrw.ResponseWriter.WriteHeader(code)
}

func ResponseMetricsHandler(rw http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	requestHoneycombEventBuilder.SendNow(map[string]interface{}{
		"event":    "started",
		"method":   req.Method,
		"endpoint": req.URL.Path,
	})

	next(&metricsResponseWriter{
		ResponseWriter: rw,
		req:            req,
		start:          time.Now(),
	}, req)
}
