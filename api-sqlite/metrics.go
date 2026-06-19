package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vin_requests_total",
		Help: "Total number of VIN decode requests.",
	}, []string{"endpoint", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vin_request_duration_seconds",
		Help:    "VIN decode request latency.",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1},
	}, []string{"endpoint"})

	schemaCacheSize = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "vpic_schema_cache_size",
		Help: "Number of WMI schema sets currently in the regex cache.",
	}, func() float64 {
		var n float64
		schemaCache.Range(func(_, _ any) bool { n++; return true })
		return n
	})
)

// metricsMiddleware wraps a handler, recording request count and duration.
func metricsMiddleware(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(rw, r)
		dur := time.Since(start).Seconds()
		status := strconv.Itoa(rw.status)
		requestsTotal.WithLabelValues(endpoint, status).Inc()
		requestDuration.WithLabelValues(endpoint).Observe(dur)
	}
}

// statusWriter captures the HTTP status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func metricsHandler() http.Handler {
	return promhttp.Handler()
}
