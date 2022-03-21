package loganalytics

import (
	"context"
	"crypto/sha1" // #nosec
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/operationalinsights/v1/operationalinsights"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/remeh/sizedwaitgroup"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/go-prometheus-common/azuretracing"
	"github.com/webdevops/go-prometheus-common/kusto"

	"github.com/webdevops/azure-loganalytics-exporter/config"
)

const (
	OperationInsightsWorkspaceUrlSuffix = "/v1"
)

type (
	LogAnalyticsProber struct {
		QueryConfig kusto.Config
		Conf        config.Opts
		UserAgent   string

		Azure struct {
			Environment          azure.Environment
			OpInsightsAuthorizer autorest.Authorizer
			AzureAuthorizer      autorest.Authorizer
		}

		workspaceList []string

		request  *http.Request
		response http.ResponseWriter

		ctx context.Context

		registry   *prometheus.Registry
		metricList *kusto.MetricList

		logger *log.Entry

		cache *cache.Cache

		config struct {
			moduleName    string
			cacheEnabled  bool
			cacheDuration *time.Duration
			cacheKey      *string
		}

		ServiceDiscovery LogAnalyticsServiceDiscovery
	}

	LogAnalyticsProbeResult struct {
		WorkspaceId string
		Name        string
		Metrics     []kusto.MetricRow
		Error       error
	}

	LogAnalyticsPanicStop struct {
		Message string
	}
)

func NewLogAnalyticsProber(w http.ResponseWriter, r *http.Request) *LogAnalyticsProber {
	prober := LogAnalyticsProber{}
	prober.workspaceList = []string{}
	prober.request = r
	prober.response = w
	prober.ctx = context.Background()
	prober.registry = prometheus.NewRegistry()

	prober.metricList = &kusto.MetricList{}
	prober.metricList.Init()

	prober.ServiceDiscovery = LogAnalyticsServiceDiscovery{
		prober: &prober,
	}

	prober.Init()

	return &prober
}

func (p *LogAnalyticsProber) Init() {
	p.config.moduleName = p.request.URL.Query().Get("module")

	p.logger = log.WithField("module", p.config.moduleName)

	cacheTime, err := p.parseCacheTime(p.request)
	if err != nil {
		p.logger.Error(err)
		panic(LogAnalyticsPanicStop{Message: err.Error()})
	}

	if cacheTime.Seconds() > 0 {
		p.config.cacheEnabled = true
		p.config.cacheDuration = &cacheTime
		p.config.cacheKey = to.StringPtr(
			fmt.Sprintf(
				"metrics:%x",
				string(sha1.New().Sum([]byte(p.request.RequestURI))),
			),
		) // #nosec
	}
}

func (p *LogAnalyticsProber) EnableCache(cache *cache.Cache) {
	p.cache = cache
}

func (p *LogAnalyticsProber) SetPrometheusRegistry(registry *prometheus.Registry) {
	p.registry = registry
}

func (p *LogAnalyticsProber) GetPrometheusRegistry() *prometheus.Registry {
	return p.registry
}

func (p *LogAnalyticsProber) AddWorkspaces(workspace ...string) {
	p.workspaceList = append(p.workspaceList, workspace...)
}

func (p *LogAnalyticsProber) LogAnalyticsQueryClient() operationalinsights.QueryClient {
	// Create and authorize operationalinsights client
	client := operationalinsights.NewQueryClientWithBaseURI(p.Azure.Environment.ResourceIdentifiers.OperationalInsights + OperationInsightsWorkspaceUrlSuffix)
	p.decorateAzureAutoRest(&client.Client)
	client.Authorizer = p.Azure.OpInsightsAuthorizer
	return client
}

func (p *LogAnalyticsProber) Run(w http.ResponseWriter, r *http.Request) {
	requestTime := time.Now()

	// check if value is cached
	executeQuery := true
	if p.cache != nil && p.config.cacheEnabled {
		if v, ok := p.cache.Get(*p.config.cacheKey); ok {
			if cacheData, ok := v.([]byte); ok {
				if err := json.Unmarshal(cacheData, &p.metricList); err == nil {
					p.logger.Debug("fetched metrics from cache")
					p.response.Header().Add("X-metrics-cached", "true")
					executeQuery = false
				} else {
					p.logger.Debug("unable to parse cached metrics")
				}
			}
		}
	}

	if executeQuery {
		p.response.Header().Add("X-metrics-cached", "false")

		if p.ServiceDiscovery.enabled {
			p.ServiceDiscovery.ServiceDiscovery()
		}

		err := p.executeQueries()
		if err != nil {
			p.logger.WithField("request", r.RequestURI).Error(err)
			w.WriteHeader(http.StatusBadRequest)
			if _, writeErr := w.Write([]byte("ERROR: " + err.Error())); writeErr != nil {
				p.logger.Error(writeErr)
			}
			return
		}

		// store to cache (if enabeld)
		if p.cache != nil && p.config.cacheEnabled {
			p.logger.Debug("saving metrics to cache")
			if cacheData, err := json.Marshal(p.metricList); err == nil {
				p.response.Header().Add("X-metrics-cached-until", time.Now().Add(*p.config.cacheDuration).Format(time.RFC3339))
				p.cache.Set(*p.config.cacheKey, cacheData, *p.config.cacheDuration)
				p.logger.Debugf("saved metric to cache for %s", p.config.cacheDuration.String())
			}
		}
	}

	p.logger.Debug("building prometheus metrics")
	for _, metricName := range p.metricList.GetMetricNames() {
		metricLabelNames := p.metricList.GetMetricLabelNames(metricName)

		gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricName,
			Help: metricName,
		}, metricLabelNames)
		p.registry.MustRegister(gaugeVec)

		for _, metric := range p.metricList.GetMetricList(metricName) {
			for _, labelName := range metricLabelNames {
				if _, ok := metric.Labels[labelName]; !ok {
					metric.Labels[labelName] = ""
				}
			}

			if metric.Value != nil {
				gaugeVec.With(metric.Labels).Set(*metric.Value)
			}
		}
	}
	p.logger.WithField("duration", time.Since(requestTime).String()).Debug("finished request")

	h := promhttp.HandlerFor(p.GetPrometheusRegistry(), promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func (p *LogAnalyticsProber) executeQueries() error {
	queryClient := p.LogAnalyticsQueryClient()

	for _, queryRow := range p.QueryConfig.Queries {
		queryConfig := queryRow

		workspaceList := p.workspaceList
		if queryRow.Workspaces != nil && len(*queryRow.Workspaces) >= 1 {
			workspaceList = *queryRow.Workspaces
		}

		if len(workspaceList) == 0 {
			return errors.New("no workspaces found")
		}

		// check if query matches module name
		if queryConfig.Module != p.config.moduleName {
			continue
		}
		startTime := time.Now()

		contextLogger := p.logger.WithField("metric", queryConfig.Metric)

		if queryConfig.Timespan == nil {
			err := fmt.Errorf("timespan missing")
			contextLogger.Error(err)
			panic(LogAnalyticsPanicStop{Message: err.Error()})
		}

		contextLogger.Debug("starting query")

		resultTotalRecords := 0

		resultChannel := make(chan LogAnalyticsProbeResult)
		wgProbes := p.NewSizedWaitGroup()

		// query workspaces
		go func() {
			switch strings.ToLower(queryRow.QueryMode) {
			case "all", "multi":
				wgProbes.Add()
				go func() {
					defer wgProbes.Done()
					p.sendQueryToMultipleWorkspace(
						contextLogger,
						workspaceList,
						queryClient,
						queryConfig,
						resultChannel,
					)
				}()
			case "", "single":
				for _, row := range workspaceList {
					workspaceId := row
					// Run the query and get the results
					prometheusQueryRequests.With(prometheus.Labels{"workspaceID": workspaceId, "module": p.config.moduleName, "metric": queryConfig.Metric}).Inc()

					wgProbes.Add()
					go func() {
						defer wgProbes.Done()
						p.sendQueryToSingleWorkspace(
							contextLogger,
							workspaceId,
							queryClient,
							queryConfig,
							resultChannel,
						)
					}()
				}
			default:
				contextLogger.Error(fmt.Errorf("invalid queryMode \"%s\"", queryRow.QueryMode))
				resultChannel <- LogAnalyticsProbeResult{
					Error: fmt.Errorf("invalid queryMode \"%s\"", queryRow.QueryMode),
				}
			}

			// wait until queries are done for closing channel and waiting for result process
			wgProbes.Wait()
			close(resultChannel)
		}()

		for result := range resultChannel {
			if result.Error == nil {
				resultTotalRecords++
				p.metricList.Add(result.Name, result.Metrics...)

				prometheusQueryStatus.With(prometheus.Labels{
					"module":      p.config.moduleName,
					"metric":      queryConfig.Metric,
					"workspaceID": result.WorkspaceId,
				}).Set(1)

				prometheusQueryLastSuccessfull.With(prometheus.Labels{
					"module":      p.config.moduleName,
					"metric":      queryConfig.Metric,
					"workspaceID": result.WorkspaceId,
				}).SetToCurrentTime()
			} else {
				prometheusQueryStatus.With(prometheus.Labels{
					"module":      p.config.moduleName,
					"metric":      queryConfig.Metric,
					"workspaceID": result.WorkspaceId,
				}).Set(0)

				contextLogger.Error(result.Error)
			}
		}

		elapsedTime := time.Since(startTime)
		contextLogger.WithField("results", resultTotalRecords).Debugf("fetched %v results", resultTotalRecords)
		prometheusQueryTime.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Observe(elapsedTime.Seconds())
		prometheusQueryResults.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Set(float64(resultTotalRecords))
	}

	return nil
}

func (p *LogAnalyticsProber) sendQueryToMultipleWorkspace(logger *log.Entry, workspaces []string, queryClient operationalinsights.QueryClient, queryConfig kusto.ConfigQuery, result chan<- LogAnalyticsProbeResult) {
	workspaceLogger := logger.WithField("workspaceId", workspaces)

	// Set options
	queryBody := operationalinsights.QueryBody{
		Query:      &queryConfig.Query,
		Timespan:   queryConfig.Timespan,
		Workspaces: &workspaces,
	}

	workspaceLogger.WithField("query", queryConfig.Query).Debug("send query to loganaltyics workspaces")
	var queryResults, queryErr = queryClient.Execute(p.ctx, workspaces[0], queryBody)
	if queryErr != nil {
		workspaceLogger.Error(queryErr.Error())
		result <- LogAnalyticsProbeResult{
			Error: queryErr,
		}
		return
	}

	logger.Debug("fetched query result")
	resultTables := *queryResults.Tables

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
					}

					result <- LogAnalyticsProbeResult{
						WorkspaceId: "",
						Name:        metricName,
						Metrics:     metric,
					}
				}
			}
		}
	}

	logger.Debug("metrics parsed")
}

func (p *LogAnalyticsProber) sendQueryToSingleWorkspace(logger *log.Entry, workspaceId string, queryClient operationalinsights.QueryClient, queryConfig kusto.ConfigQuery, result chan<- LogAnalyticsProbeResult) {
	workspaceLogger := logger.WithField("workspaceId", workspaceId)

	// Set options
	workspaces := []string{
		workspaceId,
	}
	queryBody := operationalinsights.QueryBody{
		Query:      &queryConfig.Query,
		Timespan:   queryConfig.Timespan,
		Workspaces: &workspaces,
	}

	workspaceLogger.WithField("query", queryConfig.Query).Debug("send query to loganaltyics workspace")
	var queryResults, queryErr = queryClient.Execute(p.ctx, workspaceId, queryBody)
	if queryErr != nil {
		workspaceLogger.Error(queryErr.Error())
		result <- LogAnalyticsProbeResult{
			Error: queryErr,
		}
		return
	}

	logger.Debug("fetched query result")
	resultTables := *queryResults.Tables

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

					result <- LogAnalyticsProbeResult{
						WorkspaceId: workspaceId,
						Name:        metricName,
						Metrics:     metric,
					}
				}
			}
		}
	}

	logger.Debug("metrics parsed")
}

func (p *LogAnalyticsProber) parseCacheTime(r *http.Request) (time.Duration, error) {
	durationString := r.URL.Query().Get("cache")
	if durationString != "" {
		if v, err := time.ParseDuration(durationString); err == nil {
			return v, nil
		} else {
			return 0, err
		}
	}

	return 0, nil
}

func (p *LogAnalyticsProber) NewSizedWaitGroup() sizedwaitgroup.SizedWaitGroup {
	size := p.Conf.Loganalytics.Parallel

	parallelString := p.request.URL.Query().Get("parallel")
	if parallelString != "" {
		if v, err := strconv.ParseInt(parallelString, 10, 64); err == nil {
			size = int(v)
		}
	}

	return sizedwaitgroup.New(size)
}

func (p *LogAnalyticsProber) decorateAzureAutoRest(client *autorest.Client) {
	client.Authorizer = p.Azure.AzureAuthorizer
	if err := client.AddToUserAgent(p.UserAgent); err != nil {
		log.Panic(err)
	}
	azuretracing.DecorateAzureAutoRestClient(client)
}
