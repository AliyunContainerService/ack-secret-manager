package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/restmapper"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"os"
	"runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strings"
	"time"

	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller"
	"github.com/AliyunContainerService/ack-secret-manager/version"

	"github.com/operator-framework/operator-sdk/pkg/leader"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme = k8sruntime.NewScheme()
	log    = logf.Log.WithName("cmd")
)

func init() {
	corev1.AddToScheme(scheme)
	apis.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func printVersion() {
	log.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

func main() {
	var reconcilePeriod time.Duration
	var enableLeaderElection bool
	var selectedBackend string
	var watchNamespaces string
	var excludeNamespaces string

	backendCfg := backend.Config{}

	flag.StringVar(&selectedBackend, "backend", "alicloud-kms", "Selected backend. Only alicloud-kms supported")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue secretdefinition events")
	flag.StringVar(&backendCfg.Region, "region", "cn-hangzhou", "Region id, change it according to where the cluster deployed")
	flag.DurationVar(&backendCfg.TokenRotationPeriod, "token-rotation-period", 120*time.Second, "Polling interval to check token expiration time.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that ack-secret-manager watch. By default all namespaces are watched.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces that that ack-secret-manager will not watch. By default all namespaces are watched.")
	flag.Parse()

	ctrl.SetLogger(zap.New())

	printVersion()

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	//Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	ctx := context.TODO()
	// Become the leader before proceeding
	err = leader.Become(ctx, "ack-secret-manager-lock")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backendClient, err := backend.NewBackendClient(ctx, selectedBackend, backendCfg)
	if err != nil {
		log.Error(err, "could not build backend client")
		os.Exit(1)
	}

	// Create a new Cmd to provide shared dependencies and start components
	//mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
	//	LeaderElection:     enableLeaderElection,
	//})
	//if err != nil {
	//	log.Error(err, "failed to start manager")
	//	os.Exit(1)
	//}

	mgr, err := manager.New(cfg, manager.Options{
		Namespace:      namespace,
		MapperProvider: restmapper.NewDynamicRESTMapper,
	})
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	//// Setup all Controllers
	//if err := controller.AddToManager(mgr); err != nil {
	//	log.Error(err, "")
	//	os.Exit(1)
	//}

	nsSlice := func(ns string) []string {
		trimmed := strings.Trim(strings.TrimSpace(ns), "\"")
		return strings.Split(trimmed, ",")
	}

	watchNs := make(map[string]bool)
	if len(watchNamespaces) > 0 {
		for _, ns := range nsSlice(watchNamespaces) {
			watchNs[ns] = true
		}
	}
	if len(excludeNamespaces) > 0 {
		for _, ns := range nsSlice(excludeNamespaces) {
			watchNs[ns] = false
		}
	}
	log.Info("backendClient is:", "backendClient", &backendClient)
	err = (&controller.ExternalSecretReconciler{
		Backend:              *backendClient,
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Log:                  ctrl.Log.WithName("controllers").WithName("ExternalSecret"),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
		WatchNamespaces:      watchNs,
	}).SetupWithManager(mgr)
	if err != nil {
		log.Error(err, "unable to create controller", "controller", "SecretDefinition")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	log.Info("starting ack-secret-manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "failed to run manager")
		os.Exit(1)
	}
}
