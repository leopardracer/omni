package provider

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	callbackErrTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lib",
		Subsystem: "xprovider",
		Name:      "callback_error_total",
		Help:      "Total number of callback errors per source chain version and stream type. Alert if growing.",
	}, []string{"chain_version", "type"})

	fetchErrTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lib",
		Subsystem: "xprovider",
		Name:      "fetch_error_total",
		Help:      "Total number of fetch errors per source chain version and stream type. Alert if growing.",
	}, []string{"chain_version", "type"})

	streamHeight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lib",
		Subsystem: "xprovider",
		Name:      "stream_height",
		Help:      "Latest successfully streamed height per source chain version and stream type. Alert if not growing.",
	}, []string{"chain_version", "type"})

	streamLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lib",
		Subsystem: "xprovider",
		Name:      "stream_lag_seconds",
		Help:      "Latest successfully streamed lag (since timestamp) per source chain version and stream type.",
	}, []string{"chain_version", "type"})

	callbackLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "lib",
		Subsystem: "xprovider",
		Name:      "callback_latency_seconds",
		Help:      "Callback latency in seconds per source chain version and type. Alert if growing.",
		Buckets:   []float64{.001, .002, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 25, 50, 100},
	}, []string{"chain_version", "type"})
)
