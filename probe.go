package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/azure-resourcegraph-exporter/kusto"
	"net/http"
	"time"
)

func handleProbeRequest(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(fmt.Sprintf("%v", r))
			http.Error(w, fmt.Sprintf("%v", r), http.StatusBadRequest)
			return
		}
	}()

	registry := prometheus.NewRegistry()

	requestTime := time.Now()

	params := r.URL.Query()
	moduleName := params.Get("module")
	cacheKey := "cache:" + moduleName

	probeLogger := log.WithField("module", moduleName)

	cacheTime := 0 * time.Second
	cacheTimeDurationStr := params.Get("cache")
	if cacheTimeDurationStr != "" {
		if v, err := time.ParseDuration(cacheTimeDurationStr); err == nil {
			cacheTime = v
		} else {
			probeLogger.Errorln(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	}

	ctx := context.Background()
	queryClient := NewLoganalyticsQueryClient()

	metricList := kusto.MetricList{}
	metricList.Init()

	// check if value is cached
	executeQuery := true
	if cacheTime.Seconds() > 0 {
		if v, ok := metricCache.Get(cacheKey); ok {
			if cacheData, ok := v.([]byte); ok {
				if err := json.Unmarshal(cacheData, &metricList); err == nil {
					probeLogger.Debug("fetched from cache")
					w.Header().Add("X-metrics-cached", "true")
					executeQuery = false
				} else {
					probeLogger.Debug("unable to parse cache data")
				}
			}
		}
	}

	if executeQuery {
		w.Header().Add("X-metrics-cached", "false")
		for _, queryRow := range Config.Queries {
			queryConfig := queryRow

			// check if query matches module name
			if queryConfig.Module != moduleName {
				continue
			}
			startTime := time.Now()

			contextLogger := probeLogger.WithField("metric", queryConfig.Metric)

			if queryConfig.Timespan == nil {
				err := fmt.Errorf("timespan missing")
				contextLogger.Errorln(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			contextLogger.Debug("starting query")

			resultTotalRecords := 0

			resultChannel := make(chan probeResult)
			wgProbes := NewWaitGroupWithSize(r)
			wgProcess := NewWaitGroup()

			// collect metrics
			wgProcess.Add(1)
			go func() {
				defer wgProcess.Done()
				for result := range resultChannel {
					resultTotalRecords++
					metricList.Add(result.Name, result.Metrics...)
				}
			}()

			// query workspaces
			for _, row := range opts.Loganalytics.Workspace {
				workspaceId := row
				// Run the query and get the results
				prometheusQueryRequests.With(prometheus.Labels{"workspace": workspaceId, "module": moduleName, "metric": queryConfig.Metric}).Inc()

				wgProbes.Add()
				go func() {
					defer wgProbes.Done()
					SendQueryToLoganalyticsWorkspace(
						ctx,
						contextLogger,
						workspaceId,
						queryClient,
						queryConfig,
						resultChannel,
					)
				}()
			}

			// wait until queries are done for closing channel and waiting for result process
			wgProbes.Wait()
			close(resultChannel)
			wgProcess.Wait()

			elapsedTime := time.Since(startTime)
			contextLogger.WithField("results", resultTotalRecords).Debugf("fetched %v results", resultTotalRecords)
			prometheusQueryTime.With(prometheus.Labels{"module": moduleName, "metric": queryConfig.Metric}).Observe(elapsedTime.Seconds())
			prometheusQueryResults.With(prometheus.Labels{"module": moduleName, "metric": queryConfig.Metric}).Set(float64(resultTotalRecords))
		}

		// store to cache (if enabeld)
		if cacheTime.Seconds() > 0 {
			if cacheData, err := json.Marshal(metricList); err == nil {
				w.Header().Add("X-metrics-cached-until", time.Now().Add(cacheTime).Format(time.RFC3339))
				metricCache.Set(cacheKey, cacheData, cacheTime)
				probeLogger.Debugf("saved metric to cache for %s minutes", cacheTime.String())
			}
		}
	}

	probeLogger.Debug("building prometheus metrics")
	for _, metricName := range metricList.GetMetricNames() {
		metricLabelNames := metricList.GetMetricLabelNames(metricName)

		gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricName,
			Help: metricName,
		}, metricLabelNames)
		registry.MustRegister(gaugeVec)

		for _, metric := range metricList.GetMetricList(metricName) {
			for _, labelName := range metricLabelNames {
				if _, ok := metric.Labels[labelName]; !ok {
					metric.Labels[labelName] = ""
				}
			}

			gaugeVec.With(metric.Labels).Set(metric.Value)
		}
	}
	probeLogger.WithField("duration", time.Since(requestTime).String()).Debug("finished request")

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}
