package main

import (
	"context"
	"encoding/json"
	"fmt"
	operationalinsights "github.com/Azure/azure-sdk-for-go/services/operationalinsights/v1/operationalinsights"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/azure-resourcegraph-exporter/kusto"
	"net/http"
	"time"
)

const (
	OPINSIGHTS_URL_SUFFIX = "/v1"
)

func handleProbeRequest(w http.ResponseWriter, r *http.Request) {
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

	// Create and authorize a operationalinsights client
	queryClient := operationalinsights.NewQueryClientWithBaseURI(AzureEnvironment.ResourceIdentifiers.OperationalInsights + OPINSIGHTS_URL_SUFFIX)
	queryClient.Authorizer = OpInsightsAuthorizer
	queryClient.ResponseInspector = respondDecorator()

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
		for _, queryConfig := range Config.Queries {
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
			for _, workspaceId := range opts.Loganalytics.Workspace {
				// Set options
				workspaces := []string{}
				queryBody := operationalinsights.QueryBody{
					Query:      &queryConfig.Query,
					Timespan:   queryConfig.Timespan,
					Workspaces: &workspaces,
				}

				// Run the query and get the results
				prometheusQueryRequests.With(prometheus.Labels{"workspace": workspaceId, "module": moduleName, "metric": queryConfig.Metric}).Inc()

				var results, queryErr = queryClient.Execute(ctx, workspaceId, queryBody)
				resultTotalRecords = 1

				if queryErr == nil {
					contextLogger.Debug("parsing result")
					resultTables := *results.Tables

					if len(resultTables) >= 1 {
						for _, table := range resultTables {
							if table.Rows == nil || table.Columns == nil {
								// no results found, skip table
								continue
							}

							for _, v := range *table.Rows {
								resultTotalRecords++
								resultRow := map[string]interface{}{}

								for colNum, colName := range *resultTables[0].Columns {
									resultRow[to.String(colName.Name)] = v[colNum]
								}

								for metricName, metric := range kusto.BuildPrometheusMetricList(queryConfig.Metric, queryConfig.MetricConfig, resultRow) {
									// inject workspaceId
									for num := range metric {
										metric[num].Labels["workspaceTable"] = to.String(table.Name)
										metric[num].Labels["workspaceID"] = workspaceId
									}

									metricList.Add(metricName, metric...)
								}
							}
						}
					}

					contextLogger.Debug("metrics parsed")
				} else {
					contextLogger.Errorln(queryErr.Error())
					http.Error(w, queryErr.Error(), http.StatusBadRequest)
					return
				}
			}

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

func respondDecorator() autorest.RespondDecorator {
	return func(p autorest.Responder) autorest.Responder {
		return autorest.ResponderFunc(func(r *http.Response) error {
			return nil
		})
	}
}
