package server

import (
	"fmt"
	"net/http"
	"time"

	libhoney "github.com/honeycombio/libhoney-go"
	"github.com/travis-ci/jupiter-brain/metrics"
)

type metricsResponseWriter struct {
	http.ResponseWriter

	responseCode int
	bytesWritten int64
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	metrics.Mark(fmt.Sprintf("travis.jupiter-brain.response-code.%d", code))
	mrw.responseCode = code

	mrw.ResponseWriter.WriteHeader(code)
}

func (mrw *metricsResponseWriter) Write(p []byte) (int, error) {
	n, err := mrw.ResponseWriter.Write(p)

	mrw.bytesWritten += int64(n)

	return n, err
}

func ResponseMetricsHandler(rw http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	mrw := &metricsResponseWriter{
		ResponseWriter: rw,
		responseCode:   http.StatusOK,
		bytesWritten:   0,
	}

	requestStart := time.Now()
	next(mrw, req)

	libhoney.SendNow(map[string]interface{}{
		"event":          "http-request",
		"method":         req.Method,
		"url_path":       req.URL.Path,
		"request_id":     req.Header.Get("X-Request-ID"),
		"response_code":  mrw.responseCode,
		"total_ms":       float64(time.Since(requestStart).Nanoseconds()) / 1000000.0,
		"response_bytes": mrw.bytesWritten,
	})
}
