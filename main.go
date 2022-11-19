package main

import (
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jessevdk/go-flags"
	cache "github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/remeh/sizedwaitgroup"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/go-common/azuresdk/armclient"
	"github.com/webdevops/go-common/azuresdk/prometheus/tracing"
	"github.com/webdevops/go-common/prometheus/kusto"

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

	AzureClient                *armclient.ArmClient
	AzureSubscriptionsIterator *armclient.SubscriptionsIterator

	concurrentWaitGroup sizedwaitgroup.SizedWaitGroup

	metricCache *cache.Cache

	//go:embed templates/*.html
	templates embed.FS

	// Git version information
	gitCommit = "<unknown>"
	gitTag    = "<unknown>"
)

func main() {
	initArgparser()
	initLogger()

	log.Infof("starting azure-loganalytics-exporter v%s (%s; %s; by %v)", gitTag, gitCommit, runtime.Version(), Author)
	log.Info(string(opts.GetJson()))
	loganalytics.InitGlobalMetrics()

	concurrentWaitGroup = sizedwaitgroup.New(opts.Loganalytics.Concurrency)

	metricCache = cache.New(120*time.Second, 60*time.Second)

	log.Infof("loading config")
	readConfig()

	log.Infof("init Azure")
	initAzureConnection()

	log.Infof("starting http server on %s", opts.Server.Bind)
	startHttpServer()
}

// init argparser and parse/validate arguments
func initArgparser() {
	argparser = flags.NewParser(&opts, flags.Default)
	_, err := argparser.Parse()

	// check if there is an parse error
	if err != nil {
		var flagsErr *flags.Error
		if ok := errors.As(err, &flagsErr); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		} else {
			fmt.Println()
			argparser.WriteHelp(os.Stdout)
			os.Exit(1)
		}
	}
}

func initLogger() {
	// verbose level
	if opts.Logger.Debug {
		log.SetLevel(log.DebugLevel)
	}

	// trace level
	if opts.Logger.Trace {
		log.SetReportCaller(true)
		log.SetLevel(log.TraceLevel)
		log.SetFormatter(&log.TextFormatter{
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, "/")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", f.File, f.Line)
			},
		})
	}

	// json log format
	if opts.Logger.Json {
		log.SetReportCaller(true)
		log.SetFormatter(&log.JSONFormatter{
			DisableTimestamp: true,
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, "/")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", f.File, f.Line)
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

func initAzureConnection() {
	var err error
	AzureClient, err = armclient.NewArmClientWithCloudName(*opts.Azure.Environment, log.StandardLogger())
	if err != nil {
		log.Panic(err.Error())
	}

	AzureClient.SetUserAgent(UserAgent + gitTag)
}

// start and handle prometheus handler
func startHttpServer() {
	mux := http.NewServeMux()

	// healthz
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "Ok"); err != nil {
			log.Error(err)
		}
	})

	// readyz
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "Ok"); err != nil {
			log.Error(err)
		}
	})

	// report

	tmpl := template.Must(template.ParseFS(templates, "templates/*.html"))

	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		cspNonce := base64.StdEncoding.EncodeToString([]byte(uuid.New().String()))

		w.Header().Add("Content-Type", "text/html")
		w.Header().Add("Referrer-Policy", "same-origin")
		w.Header().Add("X-Frame-Options", "DENY")
		w.Header().Add("X-XSS-Protection", "1; mode=block")
		w.Header().Add("X-Content-Type-Options", "nosniff")
		w.Header().Add("Content-Security-Policy",
			fmt.Sprintf(
				"default-src 'self'; script-src 'nonce-%[1]s'; style-src 'nonce-%[1]s'; img-src 'self' data:",
				cspNonce,
			),
		)

		templatePayload := struct {
			Nonce string
		}{
			Nonce: cspNonce,
		}

		if err := tmpl.ExecuteTemplate(w, "query.html", templatePayload); err != nil {
			log.Error(err)
		}
	})

	mux.Handle("/metrics", tracing.RegisterAzureMetricAutoClean(promhttp.Handler()))

	mux.HandleFunc("/probe", handleProbeRequest)
	mux.HandleFunc("/probe/workspace", handleProbeWorkspace)
	mux.HandleFunc("/probe/subscription", handleProbeSubscriptionRequest)

	srv := &http.Server{
		Addr:         opts.Server.Bind,
		Handler:      mux,
		ReadTimeout:  opts.Server.ReadTimeout,
		WriteTimeout: opts.Server.WriteTimeout,
	}
	log.Fatal(srv.ListenAndServe())
}
