package main

import (
	"context"
	"encoding/json"
	"fmt"
	operationalinsightsProfile "github.com/Azure/azure-sdk-for-go/profiles/latest/operationalinsights/mgmt/operationalinsights"
	"github.com/Azure/azure-sdk-for-go/services/operationalinsights/v1/operationalinsights"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/remeh/sizedwaitgroup"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/azure-resourcegraph-exporter/kusto"
	"net/http"
	"strconv"
	"time"
)

const (
	OPINSIGHTS_URL_SUFFIX = "/v1"
)

type (
	LogAnalyticsProber struct {
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

	LogAnalyticsServiceDiscovery struct {
		enabled bool
		prober  *LogAnalyticsProber
	}

	LogAnalyticsProbeResult struct {
		Name    string
		Metrics []kusto.MetricRow
		Error   error
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

		cacheKey := fmt.Sprintf(
			"cache:%s",
			p.request.RequestURI,
		)
		p.config.cacheKey = &cacheKey
		fmt.Println(*p.config.cacheKey)
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
	// Create and authorize a operationalinsights client
	client := operationalinsights.NewQueryClientWithBaseURI(AzureEnvironment.ResourceIdentifiers.OperationalInsights + OPINSIGHTS_URL_SUFFIX)
	client.Authorizer = OpInsightsAuthorizer
	client.ResponseInspector = p.respondDecorator(nil)
	return client
}

func (p *LogAnalyticsProber) Run() {
	requestTime := time.Now()

	// check if value is cached
	executeQuery := true
	if p.cache != nil && p.config.cacheEnabled {
		if v, ok := metricCache.Get(*p.config.cacheKey); ok {
			if cacheData, ok := v.([]byte); ok {
				if err := json.Unmarshal(cacheData, &p.metricList); err == nil {
					p.logger.Debug("fetched from cache")
					p.response.Header().Add("X-metrics-cached", "true")
					executeQuery = false
				} else {
					p.logger.Debug("unable to parse cache data")
				}
			}
		}
	}

	if executeQuery {
		p.response.Header().Add("X-metrics-cached", "false")

		if p.ServiceDiscovery.enabled {
			p.ServiceDiscovery.Find()
		}

		p.executeQueries()

		// store to cache (if enabeld)
		if p.cache != nil && p.config.cacheEnabled {
			p.logger.Debug("saving metrics to cache")
			if cacheData, err := json.Marshal(p.metricList); err == nil {
				p.response.Header().Add("X-metrics-cached-until", time.Now().Add(*p.config.cacheDuration).Format(time.RFC3339))
				metricCache.Set(*p.config.cacheKey, cacheData, *p.config.cacheDuration)
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

			gaugeVec.With(metric.Labels).Set(metric.Value)
		}
	}
	p.logger.WithField("duration", time.Since(requestTime).String()).Debug("finished request")
}

func (p *LogAnalyticsProber) executeQueries() {
	queryClient := p.LogAnalyticsQueryClient()

	for _, queryRow := range Config.Queries {
		queryConfig := queryRow

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
		for _, row := range p.workspaceList {
			workspaceId := row
			// Run the query and get the results
			prometheusQueryRequests.With(prometheus.Labels{"workspace": workspaceId, "module": p.config.moduleName, "metric": queryConfig.Metric}).Inc()

			wgProbes.Add()
			go func() {
				defer wgProbes.Done()
				p.sendQueryToWorkspace(
					contextLogger,
					workspaceId,
					queryClient,
					queryConfig,
					resultChannel,
				)
			}()
		}

		go func() {
			// wait until queries are done for closing channel and waiting for result process
			wgProbes.Wait()
			close(resultChannel)
		}()

		for result := range resultChannel {
			if result.Error == nil {
				resultTotalRecords++
				p.metricList.Add(result.Name, result.Metrics...)
			} else {
				contextLogger.Error(result.Error)
				panic(LogAnalyticsPanicStop{Message: result.Error.Error()})
			}
		}

		elapsedTime := time.Since(startTime)
		contextLogger.WithField("results", resultTotalRecords).Debugf("fetched %v results", resultTotalRecords)
		prometheusQueryTime.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Observe(elapsedTime.Seconds())
		prometheusQueryResults.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Set(float64(resultTotalRecords))
	}
}

func (p *LogAnalyticsProber) sendQueryToWorkspace(logger *log.Entry, workspaceId string, queryClient operationalinsights.QueryClient, queryConfig kusto.ConfigQuery, result chan<- LogAnalyticsProbeResult) {
	workspaceLogger := logger.WithField("workspaceId", workspaceId)

	// Set options
	workspaces := []string{}
	queryBody := operationalinsights.QueryBody{
		Query:      &queryConfig.Query,
		Timespan:   queryConfig.Timespan,
		Workspaces: &workspaces,
	}

	workspaceLogger.WithField("query", queryConfig.Query).Debug("send query to loganaltyics workspace")
	var results, queryErr = queryClient.Execute(p.ctx, workspaceId, queryBody)

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

						result <- LogAnalyticsProbeResult{
							Name:    metricName,
							Metrics: metric,
						}
					}
				}
			}
		}

		workspaceLogger.Debug("metrics parsed")
	} else {
		workspaceLogger.Error(queryErr.Error())
		result <- LogAnalyticsProbeResult{
			Error: queryErr,
		}
	}
}

func (p *LogAnalyticsProber) respondDecorator(subscriptionId *string) autorest.RespondDecorator {
	return func(p autorest.Responder) autorest.Responder {
		return autorest.ResponderFunc(func(r *http.Response) error {
			return nil
		})
	}
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
	size := opts.Loganalytics.Parallel

	parallelString := p.request.URL.Query().Get("parallel")
	if parallelString != "" {
		if v, err := strconv.ParseInt(parallelString, 10, 64); err == nil {
			size = int(v)
		}
	}

	return sizedwaitgroup.New(size)
}

func (sd *LogAnalyticsServiceDiscovery) ResourcesClient(subscriptionId string) *operationalinsightsProfile.WorkspacesClient {
	client := operationalinsightsProfile.NewWorkspacesClientWithBaseURI(AzureEnvironment.ResourceManagerEndpoint, subscriptionId)
	client.Authorizer = AzureAuthorizer
	client.ResponseInspector = sd.prober.respondDecorator(&subscriptionId)

	return &client
}

func (sd *LogAnalyticsServiceDiscovery) Use() {
	sd.enabled = true
}
func (sd *LogAnalyticsServiceDiscovery) Find() {
	contextLogger := sd.prober.logger.WithFields(log.Fields{
		"type": "servicediscovery",
	})

	contextLogger.Debug("requesting list for workspaces via Azure API")

	params := sd.prober.request.URL.Query()

	subscriptionList, _ := paramsGetList(params, "subscription")
	for _, subscriptionId := range subscriptionList {
		subscriptionLogger := contextLogger.WithFields(log.Fields{
			"subscription": subscriptionId,
		})

		list, err := sd.ResourcesClient(subscriptionId).List(sd.prober.ctx)
		if err != nil {
			subscriptionLogger.Error(err)
			panic(LogAnalyticsPanicStop{Message: err.Error()})
		}

		for _, val := range *list.Value {
			if val.CustomerID != nil {
				sd.prober.workspaceList = append(
					sd.prober.workspaceList,
					to.String(val.CustomerID),
				)
			}
		}
	}
}
