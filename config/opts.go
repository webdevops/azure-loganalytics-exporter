package config

import (
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"
)

type (
	Opts struct {
		// logger
		Logger struct {
			Debug   bool `           long:"debug"        env:"DEBUG"    description:"debug mode"`
			Verbose bool `short:"v"  long:"verbose"      env:"VERBOSE"  description:"verbose mode"`
			LogJson bool `           long:"log.json"     env:"LOG_JSON" description:"Switch log output to json format"`
		}

		// azure
		Azure struct {
			Environment      *string `long:"azure.environment"            env:"AZURE_ENVIRONMENT"                description:"Azure environment name" default:"AZUREPUBLICCLOUD"`
			ServiceDiscovery struct {
				CacheDuration *time.Duration `long:"azure.servicediscovery.cache"            env:"AZURE_SERVICEDISCOVERY_CACHE"                description:"Duration for caching Azure ServiceDiscovery of workspaces to reduce API calls (time.Duration)" default:"30m"`
			}
		}

		Loganalytics struct {
			Workspace   []string `long:"loganalytics.workspace"    env:"LOGANALYTICS_WORKSPACE"  env-delim:" " description:"Loganalytics workspace IDs"`
			Concurrency int      `long:"loganalytics.concurrency"  env:"LOGANALYTICS_CONCURRENCY"              description:"Specifies how many workspaces should be queried concurrently" default:"5"`
		}

		// config
		Config struct {
			Path string `long:"config" short:"c"  env:"CONFIG"   description:"Config path" required:"true"`
		}

		// general options
		ServerBind string `long:"bind"     env:"SERVER_BIND"   description:"Server address"     default:":8080"`
	}
)

func (o *Opts) GetJson() []byte {
	jsonBytes, err := json.Marshal(o)
	if err != nil {
		log.Panic(err)
	}
	return jsonBytes
}
