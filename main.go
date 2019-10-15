package main

import (
	"context"
	stdlog "log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	// annotationGKEPreemptibleKillerState is the key of the annotation to use to store the expiry datetime
	annotationGKEPreemptibleKillerState string = "acoshift/gke-preemptible-killer-state"
)

// GKEPreemptibleKillerState represents the state of gke-preemptible-killer
type GKEPreemptibleKillerState struct {
	ExpiryDatetime string `json:"expiryDatetime"`
}

var (
	// flags
	blacklist = kingpin.Flag("blacklist-hours", "List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is NOT allowed").
			Envar("BLACKLIST_HOURS").
			Default("").
			Short('b').
			String()
	drainTimeout = kingpin.Flag("drain-timeout", "Max time in second to wait before deleting a node.").
			Envar("DRAIN_TIMEOUT").
			Default("300").
			Int()
	kubeConfigPath = kingpin.Flag("kubeconfig", "Provide the path to the kube config path, usually located in ~/.kube/config. For out of cluster execution").
			Envar("KUBECONFIG").
			String()
	interval = kingpin.Flag("interval", "Time in second to wait between each node check.").
			Envar("INTERVAL").
			Default("600").
			Short('i').
			Int()
	prometheusAddress = kingpin.Flag("metrics-listen-address", "The address to listen on for Prometheus metrics requests.").
				Envar("METRICS_LISTEN_ADDRESS").
				Default(":9001").
				String()
	prometheusMetricsPath = kingpin.Flag("metrics-path", "The path to listen for Prometheus metrics requests.").
				Envar("METRICS_PATH").
				Default("/metrics").
				String()
	ttl = kingpin.Flag("ttl", "Node time-to-live in second after create.").
		Envar("TTL").
		Default("86400").
		Int()
	minTTL = kingpin.Flag("min-ttl", "Node minimum time-to-live in second after create.").
		Envar("TTL").
		Default("43200").
		Int()
	whitelist = kingpin.Flag("whitelist-hours", "List of UTC time intervals in the form of `09:00 - 12:00, 13:00 - 18:00` in which deletion is allowed and preferred.").
			Envar("WHITELIST_HOURS").
			Default("").
			Short('w').
			String()
	waitEachDeletion = kingpin.Flag("wait-each-deletion", "Wait seconds between delete each pod.").
				Envar("WAIT_EACH_DELETION").
				Default("0").
				Int()

	// define prometheus counter
	nodeTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_gke_preemptible_killer_node_totals",
			Help: "Number of processed nodes.",
		},
		[]string{"status"},
	)

	// application version
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()

	// Various internals
	whitelistInstance WhitelistInstance
)

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(nodeTotals)

	time.Local = time.UTC
	rand.Seed(time.Now().UnixNano())
}

func main() {
	kingpin.Parse()

	initializeLogger()

	kubernetes, err := NewKubernetesClient(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"),
		os.Getenv("KUBERNETES_NAMESPACE"), *kubeConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Error initializing Kubernetes client")
	}

	whitelistInstance.whitelist = *whitelist
	whitelistInstance.blacklist = *blacklist
	log.Info().Msgf("Whitelist %v", whitelistInstance.whitelist)
	log.Info().Msgf("Blacklist %v", whitelistInstance.blacklist)

	if *ttl <= 0 {
		*ttl = 86400
	}
	if *ttl < *minTTL {
		*minTTL = *ttl * 80 / 100
	}

	// start prometheus
	go func() {
		log.Info().
			Str("port", *prometheusAddress).
			Str("path", *prometheusMetricsPath).
			Msg("Serving Prometheus metrics...")

		http.Handle(*prometheusMetricsPath, promhttp.Handler())

		if err := http.ListenAndServe(*prometheusAddress, nil); err != nil {
			log.Fatal().Err(err).Msg("Starting Prometheus listener failed")
		}
	}()

	// define channel and wait group to gracefully shutdown the application
	gracefulShutdown := make(chan os.Signal)
	signal.Notify(gracefulShutdown, syscall.SIGTERM, syscall.SIGINT)
	waitGroup := &sync.WaitGroup{}

	// process nodes
	go func(waitGroup *sync.WaitGroup) {
		for {
			log.Info().Msg("Listing all preemptible nodes for cluster...")

			sleepTime := ApplyJitter(*interval)

			ctx := context.Background()
			nodes, err := kubernetes.GetPreemptibleNodes(ctx)

			if err != nil {
				log.Error().Err(err).Msg("Error while getting the list of preemptible nodes")
				log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
				time.Sleep(time.Duration(sleepTime) * time.Second)
				continue
			}

			log.Info().Msgf("Cluster has %v preemptible nodes", len(nodes.Items))

			for _, node := range nodes.Items {
				waitGroup.Add(1)
				err := processNode(kubernetes, node)
				waitGroup.Done()

				if err != nil {
					nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()
					log.Error().
						Err(err).
						Str("host", *node.Metadata.Name).
						Msg("Error while processing node")
					continue
				}
			}

			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	signalReceived := <-gracefulShutdown
	log.Info().
		Msgf("Received signal %v. Sending shutdown and waiting on goroutines...", signalReceived)

	waitGroup.Wait()

	log.Info().Msg("Shutting down...")
}

func initializeLogger() {
	// log as severity for stackdriver logging to recognize the level
	zerolog.LevelFieldName = "severity"

	// set some default fields added to all logs
	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("app", "gke-preemptible-killer").
		Str("version", version).
		Logger()

	// use zerolog for any logs sent via standard log library
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	// log startup message
	log.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msg("Starting gke-preemptible-killer...")
}

// getCurrentNodeState return the state of the node by reading its metadata annotations
func getCurrentNodeState(node *corev1.Node) (state GKEPreemptibleKillerState) {
	var ok bool

	state.ExpiryDatetime, ok = node.Metadata.Annotations[annotationGKEPreemptibleKillerState]

	if !ok {
		state.ExpiryDatetime = ""
	}
	return
}

// getDesiredNodeState define the state of the node, update node annotations if not present
func getDesiredNodeState(ctx context.Context, k KubernetesClient, node *corev1.Node) (state GKEPreemptibleKillerState, err error) {
	t := time.Unix(*node.Metadata.CreationTimestamp.Seconds, 0).UTC()
	drainTimeoutTime := time.Duration(*drainTimeout) * time.Second
	ttlTime := time.Duration(*ttl) * time.Second
	minTTLTime := time.Duration(*minTTL) * time.Second

	expiryDatetime := whitelistInstance.getExpiryDate(t.Add(minTTLTime), ttlTime-drainTimeoutTime)
	state.ExpiryDatetime = expiryDatetime.Format(time.RFC3339)

	log.Info().
		Str("host", *node.Metadata.Name).
		Msgf("Annotation not found, adding %s to %s", annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	err = k.SetNodeAnnotation(ctx, *node.Metadata.Name, annotationGKEPreemptibleKillerState, state.ExpiryDatetime)

	if err != nil {
		log.Warn().
			Err(err).
			Str("host", *node.Metadata.Name).
			Msg("Error updating node metadata")

		nodeTotals.With(prometheus.Labels{"status": "failed"}).Inc()

		return
	}

	nodeTotals.With(prometheus.Labels{"status": "annotated"}).Inc()

	return
}

// processNode returns the time to delete a node after n minutes
func processNode(k KubernetesClient, node *corev1.Node) (err error) {
	ctx := context.Background()

	// get current node state
	state := getCurrentNodeState(node)

	// set node state if doesn't already have annotations
	if state.ExpiryDatetime == "" {
		state, _ = getDesiredNodeState(ctx, k, node)
	}

	// compute time difference
	now := time.Now().UTC()
	expiryDatetime, err := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if err != nil {
		log.Error().
			Err(err).
			Str("host", *node.Metadata.Name).
			Msgf("Error parsing expiry datetime with value '%s'", state.ExpiryDatetime)
		return
	}

	timeDiff := expiryDatetime.Sub(now).Minutes()

	// check if we need to delete the node or not
	if timeDiff < 0 {
		log.Info().
			Str("host", *node.Metadata.Name).
			Msgf("Node expired %.0f minute(s) ago, deleting...", timeDiff)

		// set node unschedulable
		err = k.SetUnschedulableState(ctx, *node.Metadata.Name, true)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error setting node to unschedulable state")
			return
		}

		var projectID string
		var zone string
		projectID, zone, err = k.GetProjectIdAndZoneFromNode(ctx, *node.Metadata.Name)

		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error getting project id and zone from node")
			return
		}

		var gcloud GCloudClient
		gcloud, err = NewGCloudClient(projectID, zone)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error creating GCloud client")
			return
		}

		// drain kubernetes node
		err = k.DrainNode(ctx, *node.Metadata.Name, *drainTimeout, time.Duration(*waitEachDeletion)*time.Second)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error draining kubernetes node")
			return
		}

		// drain kube-dns from kubernetes node
		err = k.DrainKubeDNSFromNode(ctx, *node.Metadata.Name, *drainTimeout)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error draining kube-dns from kubernetes node")
			return
		}

		// delete node from kubernetes cluster
		err = k.DeleteNode(ctx, *node.Metadata.Name)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", *node.Metadata.Name).
				Msg("Error deleting node")
			return
		}

		// try delete gcloud instance
		for i := 0; i < 5; i++ {
			err = gcloud.DeleteNode(*node.Metadata.Name)
			if err != nil {
				log.Error().
					Err(err).
					Str("host", *node.Metadata.Name).
					Msg("Error deleting GCloud instance, try again in 5 seconds...")
			}
			time.Sleep(5 * time.Second)
		}
		if err != nil {
			return
		}

		nodeTotals.With(prometheus.Labels{"status": "killed"}).Inc()

		log.Info().
			Str("host", *node.Metadata.Name).
			Msg("Node deleted")

		return
	}

	nodeTotals.With(prometheus.Labels{"status": "skipped"}).Inc()

	log.Info().
		Str("host", *node.Metadata.Name).
		Msgf("%.0f minute(s) to go before kill, keeping node", timeDiff)

	return
}
