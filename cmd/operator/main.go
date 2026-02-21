// hookbase-operator is the Kubernetes operator for managing Hookbase resources.
package main

import (
	"flag"
	"os"
	"time"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/admission"
	"github.com/hookbase/hookbase-operator/internal/controller"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	_ "github.com/hookbase/hookbase-operator/internal/metrics" // Register metrics

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hookbasev1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		hookbaseAPIKey       string
		hookbaseBaseURL      string
		defaultAgentImage    string
		reconcileInterval    time.Duration
		enableWebhooks       bool
		operatorNamespace    string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&hookbaseAPIKey, "hookbase-api-key", os.Getenv("HOOKBASE_API_KEY"), "Bootstrap Hookbase API key.")
	flag.StringVar(&hookbaseBaseURL, "hookbase-base-url", envOrDefault("HOOKBASE_BASE_URL", "https://api.hookbase.app"), "Hookbase API base URL.")
	flag.StringVar(&defaultAgentImage, "default-agent-image", envOrDefault("DEFAULT_AGENT_IMAGE", "ghcr.io/hookbaseapp/hookbase-agent:latest"), "Default tunnel agent container image.")
	flag.DurationVar(&reconcileInterval, "reconcile-interval", 5*time.Minute, "Default reconcile interval for drift detection.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false, "Enable admission webhooks.")
	flag.StringVar(&operatorNamespace, "operator-namespace", envOrDefault("OPERATOR_NAMESPACE", "hookbase-system"), "Namespace where the operator is deployed.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOptions := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "hookbase-operator.hookbase.io",
	}

	if enableWebhooks {
		mgrOptions.WebhookServer = webhook.NewServer(webhook.Options{
			Port: 9443,
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Register admission webhooks
	if enableWebhooks {
		webhookServer := mgr.GetWebhookServer()
		webhookServer.Register("/validate-hookbase-io-v1alpha1", &webhook.Admission{
			Handler: admission.NewHookbaseValidator(scheme),
		})
		webhookServer.Register("/mutate-hookbase-io-v1alpha1", &webhook.Admission{
			Handler: admission.NewHookbaseDefaulter(scheme),
		})
		setupLog.Info("registered admission webhooks")
	}

	// Create SDK client manager
	clientMgr := hookbase.NewClientManager(mgr.GetClient(), hookbaseBaseURL, hookbaseAPIKey)

	// Register all controllers
	if err := (&controller.WebhookSourceReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookSource")
		os.Exit(1)
	}

	if err := (&controller.WebhookDestinationReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookDestination")
		os.Exit(1)
	}

	if err := (&controller.WebhookRouteReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookRoute")
		os.Exit(1)
	}

	if err := (&controller.WebhookTunnelReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		ClientManager:     clientMgr,
		DefaultAgentImage: defaultAgentImage,
		HookbaseBaseURL:   hookbaseBaseURL,
		RequeueAfter:      reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookTunnel")
		os.Exit(1)
	}

	if err := (&controller.WebhookTransformReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookTransform")
		os.Exit(1)
	}

	if err := (&controller.WebhookFilterReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookFilter")
		os.Exit(1)
	}

	if err := (&controller.WebhookSchemaReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WebhookSchema")
		os.Exit(1)
	}

	if err := (&controller.HookbaseCronJobReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HookbaseCronJob")
		os.Exit(1)
	}

	if err := (&controller.HookbaseAPIKeyReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		ClientManager: clientMgr,
		RequeueAfter:  reconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HookbaseAPIKey")
		os.Exit(1)
	}

	if err := (&controller.HookbaseConfigReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		ClientManager:     clientMgr,
		OperatorNamespace: operatorNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HookbaseConfig")
		os.Exit(1)
	}

	// Register Ingress controller
	if err := (&controller.IngressReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Ingress")
		os.Exit(1)
	}

	// Health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager",
		"hookbaseBaseURL", hookbaseBaseURL,
		"reconcileInterval", reconcileInterval,
		"leaderElection", enableLeaderElection,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
