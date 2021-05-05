package main

import "github.com/prometheus/client_golang/prometheus"

var (
	prometheusQueryTime     *prometheus.SummaryVec
	prometheusQueryResults  *prometheus.GaugeVec
	prometheusQueryRequests *prometheus.CounterVec
)

func initGlobalMetrics() {
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
			"workspace",
			"module",
			"metric",
		},
	)
	prometheus.MustRegister(prometheusQueryRequests)
}
