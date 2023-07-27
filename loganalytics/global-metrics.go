package loganalytics

import "github.com/prometheus/client_golang/prometheus"

var (
	prometheusQueryTime            *prometheus.SummaryVec
	prometheusQueryResults         *prometheus.GaugeVec
	prometheusQueryRequests        *prometheus.CounterVec
	prometheusQueryStatus          *prometheus.GaugeVec
	prometheusQueryLastSuccessfull *prometheus.GaugeVec
	prometheusQueryWorkspaceCount  *prometheus.GaugeVec
)

func InitGlobalMetrics() {
	prometheusQueryTime = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "azure_loganalytics_query_time",
			Help: "Azure loganalytics Query time",
		},
		[]string{
			"module",
			"metric",
		},
	)
	prometheus.MustRegister(prometheusQueryTime)

	prometheusQueryResults = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_loganalytics_query_results",
			Help: "Azure loganalytics query results",
		},
		[]string{
			"module",
			"metric",
		},
	)
	prometheus.MustRegister(prometheusQueryResults)

	prometheusQueryRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "azure_loganalytics_query_requests",
			Help: "Azure loganalytics query request count",
		},
		[]string{
			"workspaceID",
			"module",
			"metric",
		},
	)
	prometheus.MustRegister(prometheusQueryRequests)

	prometheusQueryStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_loganalytics_status",
			Help: "Azure loganalytics workspace status",
		},
		[]string{
			"workspaceID",
			"module",
			"metric",
		},
	)
	prometheus.MustRegister(prometheusQueryStatus)

	prometheusQueryLastSuccessfull = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_loganalytics_last_query_successfull",
			Help: "Azure loganalytics workspace last successfull scrape time",
		},
		[]string{
			"workspaceID",
			"module",
			"metric",
		},
	)
	prometheusQueryWorkspaceCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_loganalytics_workspace_query_count",
			Help: "Azure loganalytics workspace query count",
		},
		[]string{
			"module",
		},
	)
	prometheus.MustRegister(prometheusQueryWorkspaceCount)
}
