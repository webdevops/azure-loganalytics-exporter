package main

import (
	"context"
	operationalinsights "github.com/Azure/azure-sdk-for-go/services/operationalinsights/v1/operationalinsights"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/azure-resourcegraph-exporter/kusto"
	"net/http"
)

const (
	OPINSIGHTS_URL_SUFFIX = "/v1"
)

type (
	probeResult struct {
		Name    string
		Metrics []kusto.MetricRow
	}
)

func NewLoganalyticsQueryClient() operationalinsights.QueryClient {
	// Create and authorize a operationalinsights client
	client := operationalinsights.NewQueryClientWithBaseURI(AzureEnvironment.ResourceIdentifiers.OperationalInsights + OPINSIGHTS_URL_SUFFIX)
	client.Authorizer = OpInsightsAuthorizer
	client.ResponseInspector = respondDecorator()
	return client
}

func SendQueryToLoganalyticsWorkspace(ctx context.Context, logger *log.Entry, workspaceId string, queryClient operationalinsights.QueryClient, queryConfig kusto.ConfigQuery, result chan<- probeResult) {
	workspaceLogger := logger.WithField("workspaceId", workspaceId)

	// Set options
	workspaces := []string{}
	queryBody := operationalinsights.QueryBody{
		Query:      &queryConfig.Query,
		Timespan:   queryConfig.Timespan,
		Workspaces: &workspaces,
	}

	workspaceLogger.WithField("query", queryConfig.Query).Debug("send query to loganaltyics workspace")
	var results, queryErr = queryClient.Execute(ctx, workspaceId, queryBody)

	if queryErr == nil {
		workspaceLogger.Debug("fetched query result")
		resultTables := *results.Tables

		if len(resultTables) >= 1 {
			for _, table := range resultTables {
				if table.Rows == nil || table.Columns == nil {
					// no results found, skip table
					continue
				}

				for _, v := range *table.Rows {
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

						result <- probeResult{
							Name:    metricName,
							Metrics: metric,
						}
					}
				}
			}
		}

		workspaceLogger.Debug("metrics parsed")
	} else {
		workspaceLogger.Panic(queryErr.Error())
	}
}

func respondDecorator() autorest.RespondDecorator {
	return func(p autorest.Responder) autorest.Responder {
		return autorest.ResponderFunc(func(r *http.Response) error {
			return nil
		})
	}
}
