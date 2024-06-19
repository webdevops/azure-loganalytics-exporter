package main

import (
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/webdevops/azure-loganalytics-exporter/loganalytics"
)

func handleProbePanic(w http.ResponseWriter, r *http.Request) {
	if err := recover(); err != nil {
		switch v := err.(type) {
		case loganalytics.LogAnalyticsPanicStop:
			// log entry already sent
			msg := fmt.Sprintf("ERROR: %v", v.Message)
			http.Error(w, msg, http.StatusBadRequest)
		case error:
			logger.Error(err)
			http.Error(w, v.Error(), http.StatusBadRequest)
		default:
			msg := fmt.Sprintf("%v", err)
			logger.With(zap.String("request", r.RequestURI)).Errorf(msg)
			http.Error(w, msg, http.StatusBadRequest)
		}
	}
}

func handleProbeRequest(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	prober := NewLogAnalyticsProber(w, r)
	prober.AddWorkspaces(Opts.Loganalytics.Workspace...)
	prober.Run()
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
}

func handleProbeSubscriptionRequest(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	prober := NewLogAnalyticsProber(w, r)
	prober.ServiceDiscovery.Use()
	prober.Run()
}

func NewLogAnalyticsProber(w http.ResponseWriter, r *http.Request) *loganalytics.LogAnalyticsProber {
	prober := loganalytics.NewLogAnalyticsProber(logger, w, r, &concurrentWaitGroup)
	prober.QueryConfig = Config
	prober.Conf = Opts
	prober.UserAgent = UserAgent + gitTag
	prober.SetAzureClient(AzureClient)
	prober.EnableCache(metricCache)

	return prober
}
