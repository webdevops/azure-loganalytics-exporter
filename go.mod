module github.com/webdevops/azure-loganalytics-exporter

go 1.16

require (
	github.com/Azure/azure-sdk-for-go v51.1.0+incompatible
	github.com/Azure/go-autorest/autorest v0.11.18
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.7
	github.com/Azure/go-autorest/autorest/to v0.4.0
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/jessevdk/go-flags v1.4.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/prometheus/client_golang v1.10.0
	github.com/prometheus/common v0.23.0 // indirect
	github.com/remeh/sizedwaitgroup v1.0.0
	github.com/sirupsen/logrus v1.8.1
	github.com/webdevops/azure-resourcegraph-exporter v0.0.0-20210505184535-837efac736c6
	golang.org/x/sys v0.0.0-20210503173754-0981d6026fa6 // indirect
)
