# Azure LogAnalytics exporter

[![license](https://img.shields.io/github/license/webdevops/azure-loganalytics-exporter.svg)](https://github.com/webdevops/azure-loganalytics-exporter/blob/master/LICENSE)
[![DockerHub](https://img.shields.io/badge/DockerHub-webdevops%2Fazure--loganalytics--exporter-blue)](https://hub.docker.com/r/webdevops/azure-loganalytics-exporter/)
[![Quay.io](https://img.shields.io/badge/Quay.io-webdevops%2Fazure--loganalytics--exporter-blue)](https://quay.io/repository/webdevops/azure-loganalytics-exporter)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/azure-loganalytics-exporter)](https://artifacthub.io/packages/search?repo=azure-loganalytics-exporter)

Prometheus exporter for Azure LogAnalytics Kusto queries with configurable fields and transformations.

`azure-loganalytics-exporter` can query configured workspaces or all workspaces in one or multiple subscriptions.
The exporter can also cache metrics and servicediscovery information to reduce requests against workspaces and Azure API.

## Usage

```
Usage:
  azure-loganalytics-exporter [OPTIONS]

Application Options:
      --debug                         debug mode [$DEBUG]
  -v, --verbose                       verbose mode [$VERBOSE]
      --log.json                      Switch log output to json format [$LOG_JSON]
      --azure.environment=            Azure environment name (default: AZUREPUBLICCLOUD) [$AZURE_ENVIRONMENT]
      --azure.servicediscovery.cache= Duration for caching Azure ServiceDiscovery of workspaces to reduce API
                                      calls (time.Duration) (default: 30m) [$AZURE_SERVICEDISCOVERY_CACHE]
      --loganalytics.workspace=       Loganalytics workspace IDs [$LOGANALYTICS_WORKSPACE]
      --loganalytics.parallel=        Specifies how many workspaces should be queried in parallel (default: 5)
                                      [$LOGANALYTICS_PARALLEL]
  -c, --config=                       Config path [$CONFIG]
      --bind=                         Server address (default: :8080) [$SERVER_BIND]

Help Options:
  -h, --help                          Show this help message
```

for Azure API authentication (using ENV vars) see https://docs.microsoft.com/en-us/azure/developer/go/azure-sdk-authentication

## Configuration file

* see [example.yaml](example.yaml)

## HTTP Endpoints

| Endpoint                       | Description                                                                         |
|--------------------------------|-------------------------------------------------------------------------------------|
| `/metrics`                     | Default prometheus golang metrics                                                   |
| `/probe`                       | Execute loganalytics queries against workspaces (set on commandline/env var)        |
| `/probe/workspace`             | Execute loganalytics queries against workspaces (defined as parameter)              |
| `/probe/subscription`          | Execute loganalytics queries against workspaces (using servicediscovery)            |

HINT: parameters of type `multiple` can be either specified multiple times and/or splits multiple values by comma.

#### /probe parameters

uses predefined workspace list defined as parameter/environment variable on startup

| GET parameter          | Default                   | Required | Multiple | Description                                                          |
|------------------------|---------------------------|----------|----------|----------------------------------------------------------------------|
| `module`               |                           | no       | no       | Filter queries by module name                                        |
| `cache`                |                           | no       | no       | Use of internal metrics caching (time.Duration)                      |
| `parallel`             | `$LOGANALYTICS_PARALLEL`  | no       | no       | Number (int) of how many workspaces can be queried at the same time  |

#### /probe/workspace parameters

uses dynamically passed workspaces via HTTP query parameter

| GET parameter          | Default                   | Required | Multiple | Description                                                          |
|------------------------|---------------------------|----------|----------|----------------------------------------------------------------------|
| `module`               |                           | no       | no       | Filter queries by module name                                        |
| `workspace`            |                           | **yes**  | yes      | Workspace IDs which are probed                                       |
| `cache`                |                           | no       | no       | Use of internal metrics caching (time.Duration)                      |
| `parallel`             | `$LOGANALYTICS_PARALLEL`  | no       | no       | Number (int) of how many workspaces can be queried at the same time  |

#### /probe/subscription parameters

uses Azure service discovery to find all workspaces in one or multiple subscriptions

| GET parameter          | Default                   | Required | Multiple | Description                                                          |
|------------------------|---------------------------|----------|----------|----------------------------------------------------------------------|
| `module`               |                           | no       | no       | Filter queries by module name                                        |
| `subscription`         |                           | **yes**  | yes      | Uses all workspaces inside subscription                              |
| `cache`                |                           | no       | no       | Use of internal metrics caching (time.Duration)                      |
| `parallel`             | `$LOGANALYTICS_PARALLEL`  | no       | no       | Number (int) of how many workspaces can be queried at the same time  |

## Global metrics

available on `/metrics`

| Metric                                      | Description                                                                    |
|---------------------------------------------|--------------------------------------------------------------------------------|
| `azure_loganalytics_status`                 | Status if query was successfull (per workspace, module, metric)                |
| `azure_loganalytics_last_query_successfull` | Timestamp of last successfull query (per workspace, module, metric)            |
| `azure_loganalytics_query_time`             | Summary metric about query execution time (incl. all subqueries)               |
| `azure_loganalytics_query_results`          | Number of results from query                                                   |
| `azure_loganalytics_query_requests`         | Count of requests (eg paged subqueries) per query                              |

### Azuretracing metrics

(with 22.2.0 and later)

Azuretracing metrics collects latency and latency from azure-sdk-for-go and creates metrics and is controllable using
environment variables (eg. setting buckets, disabling metrics or disable autoreset).

| Metric                                   | Description                                                                            |
|------------------------------------------|----------------------------------------------------------------------------------------|
| `azurerm_api_ratelimit`                  | Azure ratelimit metrics (only on /metrics, resets after query due to limited validity) |
| `azurerm_api_request_*`                  | Azure request count and latency as histogram                                           |

| Environment variable                     | Example                          | Description                                              |
|------------------------------------------|----------------------------------|----------------------------------------------------------|
| `METRIC_AZURERM_API_REQUEST_BUCKETS`     | `1, 2.5, 5, 10, 30, 60, 90, 120` | Sets buckets for `azurerm_api_request` histogram metric  |
| `METRIC_AZURERM_API_REQUEST_DISABLE`     | `false`                          | Disables `azurerm_api_request_*` metric                  |
| `METRIC_AZURERM_API_RATELIMIT_DISABLE`   | `false`                          | Disables `azurerm_api_ratelimit` metric                  |
| `METRIC_AZURERM_API_RATELIMIT_AUTORESET` | `false`                          | Disables `azurerm_api_ratelimit` autoreset after fetch   |

## Examples

see [example.yaml](example.yaml) for general ingestion metrics (number of rows per second and number of bytes per second per table)

see [example.aks.yaml](example.aks.yaml) for AKS namespace ingestion metrics (number of rows per second and number of bytes per AKS namespace)

more examples of result processing can be found within [azure-resourcegraph-expoter](https://github.com/webdevops/azure-resourcegraph-exporter) (uses same processing library)

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
azure_loganalytics_operationstatus_count{OperationStatus="Succeeded",workspaceId="xxxxx-xxxx-xxxx-xxxx-xxxxxxxxx",workspaceTable="PrimaryResult"} 1
```

## Prometheus configuration

predefined workspaces (at startup via parameter/environment variable)

```yaml
- job_name: azure-loganalytics-exporter
  scrape_interval: 1m
  metrics_path: /probe
  params:
    cache: ["10m"]
    parallel: ["5"]
  static_configs:
  - targets: ["azure-loganalytics-exporter:8080"]
```

dynamic workspaces (defined in prometheus configuration)

```yaml
- job_name: azure-loganalytics-exporter
  scrape_interval: 1m
  metrics_path: /probe/workspace
  params:
    workspace:
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
    cache: ["10m"]
    parallel: ["5"]
  static_configs:
  - targets: ["azure-loganalytics-exporter:8080"]
```

find workspaces with servicediscovery via subscription

```yaml
- job_name: azure-loganalytics-exporter
  scrape_interval: 1m
  metrics_path: /probe/subscription
  params:
    subscription:
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
      - xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxx
    cache: ["10m"]
    parallel: ["5"]
  static_configs:
  - targets: ["azure-loganalytics-exporter:8080"]
```
