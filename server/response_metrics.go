package server

import (
	"fmt"
	"net/http"

	"github.com/travis-ci/worker/metrics"
)

type metricsResponseWriter struct {
	http.ResponseWriter
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	metrics.Mark(fmt.Sprintf("travis.jupiter-brain.response-code.%d", code))

	mrw.ResponseWriter.WriteHeader(code)
}

func ResponseMetricsHandler(rw http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	next(&metricsResponseWriter{rw}, req)
}
