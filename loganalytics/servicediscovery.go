package loganalytics

import (
	operationalinsightsProfile "github.com/Azure/azure-sdk-for-go/profiles/latest/operationalinsights/mgmt/operationalinsights"
	"github.com/Azure/go-autorest/autorest/to"
	log "github.com/sirupsen/logrus"
)

type (
	LogAnalyticsServiceDiscovery struct {
		enabled bool
		prober  *LogAnalyticsProber
	}
)

func (sd *LogAnalyticsServiceDiscovery) ResourcesClient(subscriptionId string) *operationalinsightsProfile.WorkspacesClient {
	client := operationalinsightsProfile.NewWorkspacesClientWithBaseURI(sd.prober.Azure.Environment.ResourceManagerEndpoint, subscriptionId)
	client.Authorizer = sd.prober.Azure.AzureAuthorizer
	client.ResponseInspector = sd.prober.respondDecorator(&subscriptionId)

	return &client
}

func (sd *LogAnalyticsServiceDiscovery) Use() {
	sd.enabled = true
}

func (sd *LogAnalyticsServiceDiscovery) Find() {
	contextLogger := sd.prober.logger

	contextLogger.Debug("requesting list for workspaces via Azure API")

	params := sd.prober.request.URL.Query()

	subscriptionList, err := ParamsGetListRequired(params, "subscription")
	if err != nil {
		contextLogger.Error(err)
		panic(LogAnalyticsPanicStop{Message: err.Error()})
	}

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
