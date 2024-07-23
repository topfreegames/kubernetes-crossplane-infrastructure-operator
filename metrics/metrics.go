package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (

	// TODO: We could add the reason of the error as a label as well
	// For while we only have support for this metric in the securityGroup controller
	ReconciliationConsecutiveErrorsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "custom_reconciliation_consecutive_errors_total",
			Help: "Total number of consecutive reconciliation errors labeled by controller, name of securityGroup/clustermesh and cluster environment",
		}, []string{"controller", "object_name", "object_environment"},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(ReconciliationConsecutiveErrorsTotal)
}
