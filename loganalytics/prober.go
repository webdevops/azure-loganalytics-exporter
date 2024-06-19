package loganalytics

import (
	"context"
	"crypto/sha1" // #nosec
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/remeh/sizedwaitgroup"
	"github.com/webdevops/go-common/azuresdk/armclient"
	"github.com/webdevops/go-common/prometheus/kusto"
	"github.com/webdevops/go-common/utils/to"
	"go.uber.org/zap"

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
			Client *armclient.ArmClient
		}

		tagManagerConfig *armclient.ResourceTagManager

		workspaceList []WorkspaceConfig

		request  *http.Request
		response http.ResponseWriter

		ctx context.Context

		registry   *prometheus.Registry
		metricList *kusto.MetricList

		logger *zap.SugaredLogger

		cache *cache.Cache

		config struct {
			moduleName    string
			optional      bool
			cacheEnabled  bool
			cacheDuration *time.Duration
			cacheKey      *string
		}

		ServiceDiscovery LogAnalyticsServiceDiscovery

		concurrencyWaitGroup *sizedwaitgroup.SizedWaitGroup
	}

	WorkspaceConfig struct {
		ResourceID string
		CustomerID string
		Labels     map[string]string
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

func NewLogAnalyticsProber(logger *zap.SugaredLogger, w http.ResponseWriter, r *http.Request, concurrencyWaitGroup *sizedwaitgroup.SizedWaitGroup) *LogAnalyticsProber {
	prober := LogAnalyticsProber{}
	prober.logger = logger
	prober.workspaceList = []WorkspaceConfig{}
	prober.request = r
	prober.response = w
	prober.ctx = context.Background()
	prober.registry = prometheus.NewRegistry()
	prober.concurrencyWaitGroup = concurrencyWaitGroup

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
	p.config.optional = p.request.URL.Query().Get("optional") == "true"

	p.logger = p.logger.With(zap.String("module", p.config.moduleName))

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
				string(sha1.New().Sum([]byte(p.request.RequestURI))), //nolint:gosec
			),
		)
	}
}

func (p *LogAnalyticsProber) SetAzureClient(client *armclient.ArmClient) {
	p.Azure.Client = client

	tagManagerConfig, err := p.Azure.Client.TagManager.ParseTagConfig(p.Conf.Azure.ResourceTags)
	if err != nil {
		p.logger.Fatal(err)
	}

	p.tagManagerConfig = tagManagerConfig
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

func (p *LogAnalyticsProber) translateWorkspaceIntoConfig(val string) WorkspaceConfig {
	workspaceConfig := WorkspaceConfig{
		Labels: map[string]string{},
	}

	val = strings.TrimSpace(val)

	if strings.HasPrefix(val, "/subscriptions/") {
		workspaceResource, err := p.ServiceDiscovery.GetWorkspace(p.ctx, val)
		if err != nil {
			p.logger.Panic(err)
		}

		workspaceConfig.ResourceID = to.String(workspaceResource.ID)
		workspaceConfig.CustomerID = to.String(workspaceResource.Properties.CustomerID)

		if resourceInfo, err := armclient.ParseResourceId(workspaceConfig.ResourceID); err == nil {
			workspaceConfig.Labels["workspaceResourceID"] = workspaceConfig.ResourceID
			workspaceConfig.Labels["workspaceResourceGroup"] = resourceInfo.ResourceGroup
			workspaceConfig.Labels["workspaceResourceName"] = resourceInfo.ResourceName

			// add custom labels
			workspaceConfig.Labels = p.tagManagerConfig.AddResourceTagsToPrometheusLabels(
				p.ctx,
				workspaceConfig.Labels,
				workspaceConfig.ResourceID,
			)
		}
	} else {
		// no resource id, must be a customer id
		workspaceConfig.CustomerID = val
	}

	return workspaceConfig
}

func (p *LogAnalyticsProber) AddWorkspaces(workspaces ...string) {
	for _, item := range workspaces {
		p.workspaceList = append(p.workspaceList, p.translateWorkspaceIntoConfig(item))
	}
}

func (p *LogAnalyticsProber) Run() {
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

		prometheusQueryWorkspaceCount.With(prometheus.Labels{"module": p.config.moduleName}).Set(float64(len(p.workspaceList)))

		if p.config.optional && len(p.workspaceList) == 0 {
			return
		}

		err := p.executeQueries()
		if err != nil {
			p.logger.With(zap.String("request", p.request.RequestURI)).Error(err)
			p.response.WriteHeader(http.StatusBadRequest)
			if _, writeErr := p.response.Write([]byte("ERROR: " + err.Error())); writeErr != nil {
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
	p.logger.With(zap.String("duration", time.Since(requestTime).String())).Debug("finished request")

	h := promhttp.HandlerFor(p.GetPrometheusRegistry(), promhttp.HandlerOpts{})
	h.ServeHTTP(p.response, p.request)
}

func (p *LogAnalyticsProber) executeQueries() error {
	for _, queryRow := range p.QueryConfig.Queries {
		queryConfig := queryRow

		workspaceList := p.workspaceList
		if queryRow.Workspaces != nil && len(*queryRow.Workspaces) >= 1 {
			workspaceList = []WorkspaceConfig{}
			for _, workspace := range *queryRow.Workspaces {
				workspaceList = append(workspaceList, p.translateWorkspaceIntoConfig(workspace))
			}
		}

		if len(workspaceList) == 0 {
			return errors.New("no workspaces found")
		}

		// check if query matches module name
		if queryConfig.Module != p.config.moduleName {
			continue
		}
		startTime := time.Now()

		contextLogger := p.logger.With(zap.String("metric", queryConfig.Metric))

		contextLogger.Debug("starting query")

		resultTotalRecords := 0

		resultChannel := make(chan LogAnalyticsProbeResult)
		wgProbes := sync.WaitGroup{}

		// query workspaces
		go func() {
			switch strings.ToLower(queryRow.QueryMode) {
			case "all", "multi":
				wgProbes.Add(1)
				p.concurrencyWaitGroup.Add()
				go func() {
					defer wgProbes.Done()
					defer p.concurrencyWaitGroup.Done()
					p.sendQueryToMultipleWorkspace(
						contextLogger,
						workspaceList,
						queryConfig,
						resultChannel,
					)
				}()
			case "", "single":
				for _, row := range workspaceList {
					workspaceConfig := row
					// Run the query and get the results
					prometheusQueryRequests.With(prometheus.Labels{"workspaceID": workspaceConfig.CustomerID, "module": p.config.moduleName, "metric": queryConfig.Metric}).Inc()

					wgProbes.Add(1)
					p.concurrencyWaitGroup.Add()
					go func() {
						defer wgProbes.Done()
						defer p.concurrencyWaitGroup.Done()
						p.sendQueryToSingleWorkspace(
							contextLogger,
							workspaceConfig,
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
		contextLogger.With(zap.Int("results", resultTotalRecords)).Debugf("fetched %v results", resultTotalRecords)
		prometheusQueryTime.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Observe(elapsedTime.Seconds())
		prometheusQueryResults.With(prometheus.Labels{"module": p.config.moduleName, "metric": queryConfig.Metric}).Set(float64(resultTotalRecords))
	}

	return nil
}

func (p *LogAnalyticsProber) queryWorkspace(workspaces []WorkspaceConfig, queryConfig kusto.ConfigQuery) (azquery.LogsClientQueryWorkspaceResponse, error) {
	clientOpts := azquery.LogsClientOptions{ClientOptions: *p.Azure.Client.NewAzCoreClientOptions()}
	logsClient, err := azquery.NewLogsClient(p.Azure.Client.GetCred(), &clientOpts)
	if err != nil {
		return azquery.LogsClientQueryWorkspaceResponse{}, err
	}

	var timespan *azquery.TimeInterval
	if queryConfig.Timespan != nil {
		tmp := azquery.TimeInterval(*queryConfig.Timespan)
		timespan = &tmp
	}

	additionalWorkspaces := []*string{}
	if len(workspaces) > 1 {
		for _, workspaceConfig := range workspaces[1:] {
			additionalWorkspaces = append(additionalWorkspaces, to.StringPtr(workspaceConfig.CustomerID))
		}
	}

	opts := azquery.LogsClientQueryWorkspaceOptions{}
	queryBody := azquery.Body{
		Query:                to.StringPtr(queryConfig.Query),
		Timespan:             timespan,
		AdditionalWorkspaces: additionalWorkspaces,
	}

	return logsClient.QueryWorkspace(p.ctx, workspaces[0].CustomerID, queryBody, &opts)
}

func (p *LogAnalyticsProber) sendQueryToMultipleWorkspace(logger *zap.SugaredLogger, workspaces []WorkspaceConfig, queryConfig kusto.ConfigQuery, result chan<- LogAnalyticsProbeResult) {
	workspaceLogger := logger.With(zap.Any("workspaceId", workspaces))

	workspaceLogger.With(zap.String("query", queryConfig.Query)).Debug("send query to logAnalytics workspaces")

	queryResults, queryErr := p.queryWorkspace(workspaces, queryConfig)
	if queryErr != nil {
		workspaceLogger.Error(queryErr.Error())
		result <- LogAnalyticsProbeResult{
			Error: queryErr,
		}
		return
	}

	logger.Debug("fetched query result")
	resultTables := queryResults.Tables

	if len(resultTables) >= 1 {
		for _, table := range resultTables {
			if table.Rows == nil || table.Columns == nil {
				// no results found, skip table
				continue
			}

			for _, v := range table.Rows {
				resultRow := map[string]interface{}{}

				for colNum, colName := range resultTables[0].Columns {
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

func (p *LogAnalyticsProber) sendQueryToSingleWorkspace(logger *zap.SugaredLogger, workspaceConfig WorkspaceConfig, queryConfig kusto.ConfigQuery, result chan<- LogAnalyticsProbeResult) {
	workspaceLogger := logger.With(zap.String("workspaceId", workspaceConfig.CustomerID))

	workspaceLogger.With(zap.String("query", queryConfig.Query)).Debug("send query to logAnalytics workspace")

	queryResults, queryErr := p.queryWorkspace([]WorkspaceConfig{workspaceConfig}, queryConfig)
	if queryErr != nil {
		workspaceLogger.Error(queryErr.Error())
		result <- LogAnalyticsProbeResult{
			Error: queryErr,
		}
		return
	}

	logger.Debug("fetched query result")
	resultTables := queryResults.Tables

	if len(resultTables) >= 1 {
		for _, table := range resultTables {
			if table.Rows == nil || table.Columns == nil {
				// no results found, skip table
				continue
			}

			for _, v := range table.Rows {
				resultRow := map[string]interface{}{}

				for colNum, colName := range resultTables[0].Columns {
					resultRow[to.String(colName.Name)] = v[colNum]
				}

				for metricName, metric := range kusto.BuildPrometheusMetricList(queryConfig.Metric, queryConfig.MetricConfig, resultRow) {
					// inject workspaceId
					for num := range metric {
						metric[num].Labels["workspaceTable"] = to.String(table.Name)
						metric[num].Labels["workspaceID"] = workspaceConfig.CustomerID

						// add labels from resource config
						if workspaceConfig.Labels != nil {
							for labelName, labelValue := range workspaceConfig.Labels {
								metric[num].Labels[labelName] = labelValue
							}
						}
					}

					result <- LogAnalyticsProbeResult{
						WorkspaceId: workspaceConfig.CustomerID,
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
