Azure LogAnalytics exporter
============================

[![license](https://img.shields.io/github/license/webdevops/azure-loganalytics-exporter.svg)](https://github.com/webdevops/azure-loganalytics-exporter/blob/master/LICENSE)
[![DockerHub](https://img.shields.io/badge/DockerHub-webdevops%2Fazure--loganalytics--exporter-blue)](https://hub.docker.com/r/webdevops/azure-loganalytics-exporter/)
[![Quay.io](https://img.shields.io/badge/Quay.io-webdevops%2Fazure--loganalytics--exporter-blue)](https://quay.io/repository/webdevops/azure-loganalytics-exporter)

Prometheus exporter for Azure LogAnalytics kusto queries with configurable fields and transformations.

Usage
-----

```
Usage:
  azure-loganalytics-exporter [OPTIONS]

Application Options:
      --debug                   debug mode [$DEBUG]
  -v, --verbose                 verbose mode [$VERBOSE]
      --log.json                Switch log output to json format [$LOG_JSON]
      --azure-environment=      Azure environment name (default: AZUREPUBLICCLOUD) [$AZURE_ENVIRONMENT]
      --loganalytics.workspace= Loganalytics workspace IDs [$LOGANALYTICS_WORKSPACE]
  -c, --config=                 Config path [$CONFIG]
      --bind=                   Server address (default: :8080) [$SERVER_BIND]

Help Options:
  -h, --help                    Show this help message
```

for Azure API authentication (using ENV vars) see https://github.com/Azure/azure-sdk-for-go#authentication

Configuration file
------------------

* see [example.yaml](example.yaml)

HTTP Endpoints
--------------

| Endpoint                       | Description                                                                         |
|--------------------------------|-------------------------------------------------------------------------------------|
| `/metrics`                     | Default prometheus golang metrics                                                   |
| `/probe`                       | Execute loganalytics queries without set module name                               |
| `/probe?module=xzy`            | Execute loganalytics queries for module `xzy`                                      |
| `/probe?module=xzy&cache=2m`   | Execute loganalytics queries for module `xzy` and enable caching for 2 minutes     |

Global metrics
--------------

| Metric                               | Description                                                                    |
|--------------------------------------|--------------------------------------------------------------------------------|
| `azure_loganalytics_query_time`      | Summary metric about query execution time (incl. all subqueries)               |
| `azure_loganalytics_query_results`   | Number of results from query                                                   |
| `azure_loganalytics_query_requests`  | Count of requests (eg paged subqueries) per query                              |


Example
-------

for examples of result processing can be found within [azure-resourcegraph-expoter](https://github.com/webdevops/azure-resourcegraph-exporter) (uses same processing library)

Config file:
```
queries:
  - metric: azure_loganalytics_operationstatus_count
    query: |-
      Operation
      | summarize count() by OperationStatus
    fields:
      - name: count_
        type: value

```

Metrics:
```
# HELP azure_loganalytics_operationstatus_count azure_loganalytics_operationstatus_count
# TYPE azure_loganalytics_operationstatus_count gauge
azure_loganalytics_operationstatus_count{OperationStatus="Succeeded",workspaceId="xxxxx-xxxx-xxxx-xxxx-xxxxxxxxx"} 1
```
