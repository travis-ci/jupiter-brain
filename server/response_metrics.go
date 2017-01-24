package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	libhoney "github.com/honeycombio/libhoney-go"
	"github.com/pkg/errors"
	"github.com/travis-ci/jupiter-brain/metrics"
)

func honeySendEvent(data map[string]interface{}) error {
	ev := libhoney.NewEvent()
	ev.Dataset = "jupiter-brain-requests"

	if err := ev.Add(data); err != nil {
		return errors.Wrap(err, "adding data to event failed")
	}

	if err := ev.Send(); err != nil {
		return errors.Wrap(err, "sending event failed")
	}

	return nil
}

type metricsResponseWriter struct {
	http.ResponseWriter

	req   *http.Request
	start time.Time
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	metrics.Mark(fmt.Sprintf("travis.jupiter-brain.response-code.%d", code))

	err := honeySendEvent(map[string]interface{}{
		"event":         "finished",
		"duration_ms":   float64(time.Now().Sub(mrw.start).Nanoseconds()) / 1000000.0,
		"method":        mrw.req.Method,
		"endpoint":      mrw.req.URL.Path,
		"response_code": code,
	})
	if err != nil {
		logrus.WithField("err", err).Info("error sending event=finished to honeycomb")
	}

	mrw.ResponseWriter.WriteHeader(code)
}

func ResponseMetricsHandler(rw http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	err := honeySendEvent(map[string]interface{}{
		"event":    "started",
		"method":   req.Method,
		"endpoint": req.URL.Path,
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
