package main

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/azure-loganalytics-exporter/loganalytics"
	"net/http"
)

func handleProbePanic(w http.ResponseWriter, r *http.Request) {
	if err := recover(); err != nil {
		switch v := err.(type) {
		case loganalytics.LogAnalyticsPanicStop:
			// log entry already sent
			msg := fmt.Sprintf("ERROR: %v", v.Message)
			http.Error(w, msg, http.StatusBadRequest)
		case *log.Entry:
			// log entry already sent
			http.Error(w, v.Message, http.StatusBadRequest)
		case error:
			log.Error(err)
			http.Error(w, v.Error(), http.StatusBadRequest)
		default:
			msg := fmt.Sprintf("%v", err)
			log.Errorf(msg)
			http.Error(w, msg, http.StatusBadRequest)
		}
	}
}

func handleProbeRequest(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	if len(opts.Loganalytics.Workspace) == 0 {
		panic("no workspaces defined")
	}

	prober := NewLogAnalyticsProber(w, r)
	prober.AddWorkspaces(opts.Loganalytics.Workspace...)
	prober.Run()

	h := promhttp.HandlerFor(prober.GetPrometheusRegistry(), promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func handleProbeWorkspace(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	workspaceList, err := loganalytics.ParamsGetListRequired(r.URL.Query(), "workspace")
	if err != nil {
		panic("no workspaces defined")
	}

	prober := NewLogAnalyticsProber(w, r)
	prober.AddWorkspaces(workspaceList...)
	prober.Run()

	h := promhttp.HandlerFor(prober.GetPrometheusRegistry(), promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func handleProbeSubscriptionRequest(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	prober := NewLogAnalyticsProber(w, r)
	prober.ServiceDiscovery.Use()
	prober.Run()

	h := promhttp.HandlerFor(prober.GetPrometheusRegistry(), promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func NewLogAnalyticsProber(w http.ResponseWriter, r *http.Request) *loganalytics.LogAnalyticsProber {
	prober := loganalytics.NewLogAnalyticsProber(w, r)
	prober.QueryConfig = Config
	prober.Conf = opts
	prober.Azure.AzureAuthorizer = AzureAuthorizer
	prober.Azure.OpInsightsAuthorizer = OpInsightsAuthorizer
	prober.Azure.Environment = AzureEnvironment
	prober.EnableCache(metricCache)

	return prober
}
