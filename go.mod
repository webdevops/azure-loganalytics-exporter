module github.com/webdevops/azure-loganalytics-exporter

go 1.16

require (
	github.com/Azure/azure-sdk-for-go v54.0.0+incompatible
	github.com/Azure/go-autorest/autorest v0.11.18
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.7
	github.com/Azure/go-autorest/autorest/to v0.4.0
	github.com/jessevdk/go-flags v1.5.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/prometheus/client_golang v1.10.0
	github.com/remeh/sizedwaitgroup v1.0.0
	github.com/sirupsen/logrus v1.8.1
	github.com/webdevops/azure-resourcegraph-exporter v0.0.0-20210506193626-16892efd3376
	golang.org/x/crypto v0.0.0-20210506145944-38f3c27a63bf // indirect
	golang.org/x/sys v0.0.0-20210507161434-a76c4d0a0096 // indirect
)
