package main

import (
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/webdevops/azure-loganalytics-exporter/loganalytics"
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
			log.WithField("request", r.RequestURI).Errorf(msg)
			http.Error(w, msg, http.StatusBadRequest)
		}
	}
}

func handleProbeRequest(w http.ResponseWriter, r *http.Request) {
	defer handleProbePanic(w, r)

	prober := NewLogAnalyticsProber(w, r)
	prober.AddWorkspaces(opts.Loganalytics.Workspace...)
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
	prober := loganalytics.NewLogAnalyticsProber(w, r, &concurrentWaitGroup)
	prober.QueryConfig = Config
	prober.Conf = opts
	prober.UserAgent = UserAgent + gitTag
	prober.Azure.Client = AzureClient
	prober.EnableCache(metricCache)

	return prober
}
