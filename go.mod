module github.com/webdevops/azure-loganalytics-exporter

go 1.16

require (
	github.com/Azure/azure-sdk-for-go v56.3.0+incompatible
	github.com/Azure/go-autorest/autorest v0.11.20
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.8
	github.com/Azure/go-autorest/autorest/to v0.4.0
	github.com/jessevdk/go-flags v1.5.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/prometheus/client_golang v1.11.0
	github.com/remeh/sizedwaitgroup v1.0.0
	github.com/sirupsen/logrus v1.8.1
	github.com/webdevops/azure-resourcegraph-exporter v0.0.0-20210826200325-345c764362cc
)
