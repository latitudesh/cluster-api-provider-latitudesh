package main

import (
	"flag"
	"os"

	"github.com/go-logr/logr"
	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	controllers "github.com/latitudesh/cluster-api-provider-latitudesh/internal/controller"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/latitude"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog logr.Logger
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		watchNamespaceFlag   string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election for controller manager.")
	flag.StringVar(&watchNamespaceFlag, "watch-namespace", "", "If set, restrict the cache to this namespace.")
	zopts := zap.Options{Development: true}
	zopts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	setupLog = ctrl.Log.WithName("setup")

	cfg := ctrl.GetConfigOrDie()

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "eb05f637.cluster.x-k8s.io",
	}

	if ns := envOr("WATCH_NAMESPACE", watchNamespaceFlag); ns != "" {
		mgrOpts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				ns: {},
			},
		}
	}

	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize Latitude.sh client
	latitudeClient, err := latitude.NewClient()
	if err != nil {
		setupLog.Error(err, "unable to create Latitude.sh client")
		os.Exit(1)
	}

	// LatitudeMachine controller
	if err = (&controllers.LatitudeMachineReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		LatitudeClient: latitudeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LatitudeMachine")
		os.Exit(1)
	}

	// LatitudeCluster controller
	if err = (&controllers.LatitudeClusterReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		LatitudeClient: latitudeClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LatitudeCluster")
		os.Exit(1)
	}

	// CloudflareLoadBalancer controller
	if err = (&controllers.CloudflareLoadBalancerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CloudflareLoadBalancer")
		os.Exit(1)
	}

	// MetalLBConfig controller
	if err = (&controllers.MetalLBConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MetalLBConfig")
		os.Exit(1)
	}

	// health/readiness
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
