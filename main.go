package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/jessevdk/go-flags"
	cache "github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/go-prometheus-common/azuretracing"
	"github.com/webdevops/go-prometheus-common/kusto"

	"github.com/webdevops/azure-loganalytics-exporter/config"
	"github.com/webdevops/azure-loganalytics-exporter/loganalytics"
)

const (
	Author = "webdevops.io"

	UserAgent = "azure-loganalytics-exporter/"
)

var (
	argparser *flags.Parser
	opts      config.Opts

	Config kusto.Config

	AzureAuthorizer      autorest.Authorizer
	OpInsightsAuthorizer autorest.Authorizer
	AzureEnvironment     azure.Environment

	metricCache *cache.Cache

	// Git version information
	gitCommit = "<unknown>"
	gitTag    = "<unknown>"
)

func main() {
	initArgparser()

	log.Infof("starting azure-loganalytics-exporter v%s (%s; %s; by %v)", gitTag, gitCommit, runtime.Version(), Author)
	log.Info(string(opts.GetJson()))
	loganalytics.InitGlobalMetrics()

	metricCache = cache.New(120*time.Second, 60*time.Second)

	log.Infof("loading config")
	readConfig()

	log.Infof("init Azure")
	initAzureConnection()

	log.Infof("starting http server on %s", opts.ServerBind)
	startHttpServer()
}

// init argparser and parse/validate arguments
func initArgparser() {
	argparser = flags.NewParser(&opts, flags.Default)
	_, err := argparser.Parse()

	// check if there is an parse error
	if err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		} else {
			fmt.Println()
			argparser.WriteHelp(os.Stdout)
			os.Exit(1)
		}
	}

	// verbose level
	if opts.Logger.Verbose {
		log.SetLevel(log.DebugLevel)
	}

	// debug level
	if opts.Logger.Debug {
		log.SetReportCaller(true)
		log.SetLevel(log.TraceLevel)
		log.SetFormatter(&log.TextFormatter{
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, ".")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
			},
		})
	}

	// json log format
	if opts.Logger.LogJson {
		log.SetReportCaller(true)
		log.SetFormatter(&log.JSONFormatter{
			DisableTimestamp: true,
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, ".")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
			},
		})
	}
}

func readConfig() {
	log.Infof("read config %s", opts.Config.Path)
	Config = kusto.NewConfig(opts.Config.Path)

	if err := Config.Validate(); err != nil {
		log.Panic(err)
	}
}

// Init and build Azure authorzier
func initAzureConnection() {
	var err error

	AzureEnvironment, err = azure.EnvironmentFromName(*opts.Azure.Environment)
	if err != nil {
		log.Panic(err)
	}

	// setup azure authorizer
	AzureAuthorizer, err = auth.NewAuthorizerFromEnvironment()
	if err != nil {
		log.Panic(err)
	}

	OpInsightsAuthorizer, err = auth.NewAuthorizerFromEnvironmentWithResource(AzureEnvironment.ResourceIdentifiers.OperationalInsights)
	if err != nil {
		log.Panic(err)
	}
}

// start and handle prometheus handler
func startHttpServer() {
	// healthz
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "Ok"); err != nil {
			log.Error(err)
		}
	})

	// report
	reportTmpl := template.Must(template.ParseFiles("./templates/query.html"))
	http.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/html")
		w.Header().Add("Content-Security-Policy", "default-src 'self'; script-src-elem 'unsafe-inline' cdnjs.cloudflare.com; style-src * data: 'unsafe-inline'; style-src-elem 'self' data: 'unsafe-inline' cdnjs.cloudflare.com; img-src 'self' data:")

		if err := reportTmpl.Execute(w, nil); err != nil {
			log.Error(err)
		}
	})

	http.Handle("/metrics", azuretracing.RegisterAzureMetricAutoClean(promhttp.Handler()))

	http.HandleFunc("/probe", handleProbeRequest)
	http.HandleFunc("/probe/workspace", handleProbeWorkspace)
	http.HandleFunc("/probe/subscription", handleProbeSubscriptionRequest)

	log.Fatal(http.ListenAndServe(opts.ServerBind, nil))
}
