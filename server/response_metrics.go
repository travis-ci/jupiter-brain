package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	libhoney "github.com/honeycombio/libhoney-go"
	"github.com/travis-ci/jupiter-brain/metrics"
)

type metricsResponseWriter struct {
	http.ResponseWriter

	req   *http.Request
	start time.Time
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	metrics.Mark(fmt.Sprintf("travis.jupiter-brain.response-code.%d", code))

	err := libhoney.SendNow(map[string]interface{}{
		"event":         "finished",
		"duration_ms":   float64(time.Now().Sub(mrw.start).Nanoseconds()) / 1000000.0,
		"method":        mrw.req.Method,
		"endpoint":      mrw.req.URL.Path,
		"request_id":    mrw.req.Header.Get("X-Request-ID"),
		"response_code": code,
	})
	if err != nil {
		logrus.WithField("err", err).Info("error sending event=finished to honeycomb")
	}

	mrw.ResponseWriter.WriteHeader(code)
}

func ResponseMetricsHandler(rw http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	err := libhoney.SendNow(map[string]interface{}{
		"event":      "started",
		"method":     req.Method,
		"endpoint":   req.URL.Path,
		"request_id": req.Header.Get("X-Request-ID"),
	})
	if err != nil {
		logrus.WithField("err", err).Info("error sending event=started to honeycomb")
	}

	next(&metricsResponseWriter{
		ResponseWriter: rw,
		req:            req,
		start:          time.Now(),
	}, req)
}
