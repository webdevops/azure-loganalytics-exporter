package loganalytics

import (
	"context"
	"crypto/sha1" // #nosec
	"encoding/json"
	"fmt"
	"strings"
	"time"

	operationalinsightsProfile "github.com/Azure/azure-sdk-for-go/profiles/latest/operationalinsights/mgmt/operationalinsights"
	"github.com/Azure/go-autorest/autorest/to"
	log "github.com/sirupsen/logrus"
	azureCommon "github.com/webdevops/go-common/azure"
)

type (
	LogAnalyticsServiceDiscovery struct {
		enabled bool
		prober  *LogAnalyticsProber
	}
)

func (sd *LogAnalyticsServiceDiscovery) ResourcesClient(subscriptionId string) *operationalinsightsProfile.WorkspacesClient {
	prober := sd.prober

	client := operationalinsightsProfile.NewWorkspacesClientWithBaseURI(prober.Azure.Client.Environment.ResourceManagerEndpoint, subscriptionId)
	prober.decorateAzureAutoRest(&client.Client)

	return &client
}

func (sd *LogAnalyticsServiceDiscovery) Use() {
	sd.enabled = true
}

func (sd *LogAnalyticsServiceDiscovery) IsCacheEnabled() bool {
	prober := sd.prober
	return prober.cache != nil && prober.Conf.Azure.ServiceDiscovery.CacheDuration != nil && prober.Conf.Azure.ServiceDiscovery.CacheDuration.Seconds() > 0
}

func (sd *LogAnalyticsServiceDiscovery) GetWorkspace(ctx context.Context, resourceId string) (*operationalinsightsProfile.Workspace, error) {
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
			if cacheData, ok := v.(operationalinsightsProfile.Workspace); ok {
				fmt.Println("from cache: " + resourceId)
				return &cacheData, nil
			}
		}
	}

	resourceInfo, err := azureCommon.ParseResourceId(resourceId)
	if err != nil {
		return nil, err
	}

	workspace, err := sd.ResourcesClient(resourceInfo.Subscription).Get(ctx, resourceInfo.ResourceGroup, resourceInfo.ResourceName)
	if err != nil {
		return nil, err
	}

	if serviceDiscoveryCacheDuration != nil {
		fmt.Println("to cache: " + resourceId)
		prober.cache.Set(cacheKey, workspace, *serviceDiscoveryCacheDuration)
	}

	return &workspace, nil
}

func (sd *LogAnalyticsServiceDiscovery) ServiceDiscovery() {
	var serviceDiscoveryCacheDuration *time.Duration
	cacheKey := ""
	prober := sd.prober

	contextLogger := prober.logger

	params := prober.request.URL.Query()

	subscriptionList, err := ParamsGetListRequired(params, "subscription")
	if err != nil {
		contextLogger.Error(err)
		panic(LogAnalyticsPanicStop{Message: err.Error()})
	}

	if sd.IsCacheEnabled() {
		serviceDiscoveryCacheDuration = prober.Conf.Azure.ServiceDiscovery.CacheDuration
		cacheKey = fmt.Sprintf(
			"sd:%x",
			string(sha1.New().Sum([]byte(fmt.Sprintf("%v", subscriptionList)))),
		) // #nosec

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
	sd.requestWorkspacesFromAzure(contextLogger, subscriptionList)

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

func (sd *LogAnalyticsServiceDiscovery) requestWorkspacesFromAzure(logger *log.Entry, subscriptionList []string) {
	prober := sd.prober

	for _, subscriptionId := range subscriptionList {
		subscriptionLogger := logger.WithFields(log.Fields{
			"subscription": subscriptionId,
		})

		list, err := sd.ResourcesClient(subscriptionId).List(prober.ctx)
		if err != nil {
			subscriptionLogger.Error(err)
			panic(LogAnalyticsPanicStop{Message: err.Error()})
		}

		for _, val := range *list.Value {
			if val.CustomerID != nil {
				prober.workspaceList = append(
					prober.workspaceList,
					to.String(val.CustomerID),
				)
			}
		}
	}

}
