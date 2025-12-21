package loganalytics

import (
	"context"
	"crypto/sha1" // #nosec
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
	"github.com/webdevops/go-common/azuresdk/armclient"
	"github.com/webdevops/go-common/log/slogger"
)

type (
	LogAnalyticsServiceDiscovery struct {
		enabled bool
		prober  *LogAnalyticsProber
	}
)

func (sd *LogAnalyticsServiceDiscovery) ResourcesClient(subscriptionId string) *armoperationalinsights.WorkspacesClient {
	prober := sd.prober
	azureClient := prober.Azure.Client

	client, err := armoperationalinsights.NewWorkspacesClient(subscriptionId, azureClient.GetCred(), azureClient.NewArmClientOptions())
	if err != nil {
		prober.logger.Panic(err.Error())
	}

	return client
}

func (sd *LogAnalyticsServiceDiscovery) Use() {
	sd.enabled = true
}

func (sd *LogAnalyticsServiceDiscovery) IsCacheEnabled() bool {
	prober := sd.prober
	return prober.cache != nil && prober.Conf.Azure.ServiceDiscovery.CacheDuration != nil && prober.Conf.Azure.ServiceDiscovery.CacheDuration.Seconds() > 0
}

func (sd *LogAnalyticsServiceDiscovery) GetWorkspace(ctx context.Context, resourceId string) (*armoperationalinsights.Workspace, error) {
	var serviceDiscoveryCacheDuration *time.Duration
	cacheKey := ""
	prober := sd.prober

	if sd.IsCacheEnabled() {
		serviceDiscoveryCacheDuration = prober.Conf.Azure.ServiceDiscovery.CacheDuration
		cacheKey = fmt.Sprintf(
			"sd:workspace:%x",
			strings.ToLower(resourceId),
		) // #nosec

		// try cache
		if v, ok := prober.cache.Get(cacheKey); ok {
			if cacheData, ok := v.(*armoperationalinsights.Workspace); ok {
				fmt.Println("from cache: " + resourceId)
				return cacheData, nil
			}
		}
	}

	resourceInfo, err := armclient.ParseResourceId(resourceId)
	if err != nil {
		return nil, err
	}

	workspace, err := sd.ResourcesClient(resourceInfo.Subscription).Get(ctx, resourceInfo.ResourceGroup, resourceInfo.ResourceName, nil)
	if err != nil {
		return nil, err
	}

	if serviceDiscoveryCacheDuration != nil {
		prober.cache.Set(cacheKey, &workspace.Workspace, *serviceDiscoveryCacheDuration)
	}

	return &workspace.Workspace, nil
}

func (sd *LogAnalyticsServiceDiscovery) ServiceDiscovery() {
	var serviceDiscoveryCacheDuration *time.Duration
	cacheKey := ""
	prober := sd.prober

	contextLogger := prober.logger

	params := prober.request.URL.Query()

	subscriptionList, err := ParamsGetListRequired(params, "subscription")
	if err != nil {
		contextLogger.Error(err.Error())
		panic(LogAnalyticsPanicStop{Message: err.Error()})
	}

	if sd.IsCacheEnabled() {
		serviceDiscoveryCacheDuration = prober.Conf.Azure.ServiceDiscovery.CacheDuration
		cacheKey = fmt.Sprintf(
			"sd:%x",
			string(sha1.New().Sum([]byte(fmt.Sprintf("%v:%v", subscriptionList, prober.request.URL.String())))), //nolint:gosec
		)

		// try cache
		if v, ok := prober.cache.Get(cacheKey); ok {
			if cacheData, ok := v.([]byte); ok {
				if err := json.Unmarshal(cacheData, &prober.workspaceList); err == nil {
					contextLogger.Debug("fetched servicediscovery from cache")
					prober.response.Header().Add("X-servicediscovery-cached", "true")
					return
				} else {
					prober.logger.Debug("unable to parse cached servicediscovery")
				}
			}
		}
	}

	contextLogger.Debug("requesting list for workspaces via Azure API")
	sd.findWorkspaces(contextLogger, subscriptionList)

	// store to cache (if enabeld)
	if serviceDiscoveryCacheDuration != nil {
		contextLogger.Debug("saving servicedisccovery to cache")
		if cacheData, err := json.Marshal(prober.workspaceList); err == nil {
			prober.response.Header().Add("X-servicediscovery-cached-until", time.Now().Add(*serviceDiscoveryCacheDuration).Format(time.RFC3339))
			prober.cache.Set(cacheKey, cacheData, *serviceDiscoveryCacheDuration)
			contextLogger.Debugf("saved servicediscovery to cache for %s", serviceDiscoveryCacheDuration.String())
		}
	}
}

func (sd *LogAnalyticsServiceDiscovery) findWorkspaces(logger *slogger.Logger, subscriptionList []string) {
	prober := sd.prober

	query := "resources \n"
	query += "| where type =~ \"Microsoft.OperationalInsights/workspaces\" \n"
	if filter := prober.request.URL.Query().Get("filter"); len(filter) > 0 {
		filter = strings.TrimSpace(filter)
		filter = strings.TrimLeft(filter, "|")
		if len(filter) >= 1 {
			query += fmt.Sprintf("| %s \n", filter)
		}
	}
	query += "| project id, customerId=properties.customerId"

	opts := armclient.ResourceGraphOptions{
		Subscriptions: subscriptionList,
	}

	result, err := prober.Azure.Client.ExecuteResourceGraphQuery(
		prober.ctx,
		query,
		opts,
	)
	if err != nil {
		logger.Panic(err.Error())
	}

	for _, row := range result {
		prober.AddWorkspaces(row["id"].(string))
	}
}
