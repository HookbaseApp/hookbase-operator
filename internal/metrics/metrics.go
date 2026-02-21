// Package metrics provides Prometheus metrics for the Hookbase operator.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ReconcileTotal counts reconciliation attempts by kind and result.
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hookbase_operator_reconcile_total",
			Help: "Total number of reconciliations by kind and result.",
		},
		[]string{"kind", "result"},
	)

	// ReconcileDuration measures reconciliation duration by kind.
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hookbase_operator_reconcile_duration_seconds",
			Help:    "Duration of reconciliation in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"kind"},
	)

	// APICallsTotal counts Hookbase API calls.
	APICallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hookbase_operator_api_calls_total",
			Help: "Total number of Hookbase API calls.",
		},
		[]string{"resource", "method", "status_code"},
	)

	// ManagedResources tracks the number of managed resources by kind and namespace.
	ManagedResources = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hookbase_operator_managed_resources",
			Help: "Number of managed Hookbase resources by kind and namespace.",
		},
		[]string{"kind", "namespace"},
	)

	// TunnelConnected tracks tunnel connection status.
	TunnelConnected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hookbase_operator_tunnel_connected",
			Help: "Whether a tunnel agent is connected (0/1).",
		},
		[]string{"name", "namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		ReconcileTotal,
		ReconcileDuration,
		APICallsTotal,
		ManagedResources,
		TunnelConnected,
	)
}
