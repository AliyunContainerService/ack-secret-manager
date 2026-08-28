/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	klog "k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	_ "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/kms"
	_ "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/oos"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/clusterexternalsecret"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/externalsecret"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/secret"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/secretstore"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/serviceaccount"
	"github.com/AliyunContainerService/ack-secret-manager/version"
)

var (
	scheme = k8sruntime.NewScheme()
	log    = logf.Log.WithName("cmd")
)

func init() {
	_ = corev1.AddToScheme(scheme)
	_ = apis.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func printVersion() {
	log.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

func main() {
	var reconcilePeriod time.Duration
	var rotationInterval time.Duration
	var reconcileCount int
	var disablePolling bool
	var selectedBackend string
	var watchNamespaces string
	var excludeNamespaces string
	var region string
	var clusterId string
	var uid string
	var tokenRotationPeriod time.Duration
	var maxConcurrentSecretPulls int
	var maxConcurrentKmsSecretPulls int
	var maxConcurrentOosSecretPulls int
	var enableWorkerRole bool
	var kmsEndpoint string
	var cleanUpSecretOnFailure bool
	var processClusterSecretStore bool
	var processClusterExternalSecret bool
	var enableCrossNamespaceSecretStore bool
	var enableCrossNamespaceAuthRef bool

	flag.StringVar(&selectedBackend, "backend", "alicloud-kms", "Selected backend. Only alicloud-kms supported")
	flag.DurationVar(&rotationInterval, "polling-interval", 120*time.Second, "How often the controller will sync existing secret from kms")
	flag.BoolVar(&disablePolling, "disable-polling", false, "Disable auto polling external secret from kms.")
	flag.DurationVar(&tokenRotationPeriod, "token-rotation-period", 0, "Deprecated: this flag is ignored and kept only for compatibility with old manifests.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue externalsecret events")
	flag.IntVar(&reconcileCount, "reconcile-count", 1, "The max concurrency reconcile work at the same time")
	flag.StringVar(&region, "region", "", "Region id, change it according to where you want to pull the secret from")
	flag.StringVar(&clusterId, "cluster-id", "", "Cluster ID for deployment")
	flag.StringVar(&uid, "uid", "", "RAM User ID for the deployment cluster")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that ack-secret-manager watch. By default all namespaces are watched.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces that that ack-secret-manager will not watch. By default all namespaces are watched.")
	flag.IntVar(&maxConcurrentSecretPulls, "max-concurrent-secret-pulls", 10, "deprecated: use max-concurrent-kms-secret-pulls instead; only takes effect when max-concurrent-kms-secret-pulls is not set.")
	flag.IntVar(&maxConcurrentKmsSecretPulls, "max-concurrent-kms-secret-pulls", 10, "used to control the maximum number of kms secret pulls per second (rate limit).")
	flag.IntVar(&maxConcurrentOosSecretPulls, "max-concurrent-oos-secret-pulls", 10, "used to control the maximum number of oos secret pulls per second (rate limit).")
	flag.BoolVar(&enableWorkerRole, "enable-worker-role", false, "Enable WorkerRole (ECS RAM Role) authentication as the last tier of the auth chain. Set to true for ACK clusters where the node RAM role has KMS access.")
	flag.StringVar(&kmsEndpoint, "kms-endpoint", "", "KMS endpoint")
	flag.BoolVar(&cleanUpSecretOnFailure, "cleanup-secret-on-failure", false, "delete the corresponding cluster Secret when all data sources fail to sync (no data available), including ExternalSecrets with templates; on partial failures the Secret is never deleted and is handled by the merge/fail-closed strategy instead.")
	flag.BoolVar(&processClusterSecretStore, "process-cluster-secret-store", true, "Enable processing of ClusterSecretStore resources")
	flag.BoolVar(&processClusterExternalSecret, "process-cluster-external-secret", true, "Enable processing of ClusterExternalSecret resources")
	flag.BoolVar(&enableCrossNamespaceSecretStore, "enable-cross-namespace-secret-store", false, "Enable cross namespace SecretStore reference in ExternalSecret. Set to false to disable.")
	flag.BoolVar(&enableCrossNamespaceAuthRef, "enable-cross-namespace-auth-ref", false, "Enable cross namespace AuthRef reference in SecretStore. Set to false to disable.")

	flag.Parse()

	backend.EnableWorkerRole = enableWorkerRole
	common.EnableCrossNamespaceAuthRef = enableCrossNamespaceAuthRef

	finalMaxConcurrentSecretPulls := maxConcurrentKmsSecretPulls

	deprecatedPullsSet, kmsPullsSet := false, false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-concurrent-secret-pulls":
			deprecatedPullsSet = true
		case "max-concurrent-kms-secret-pulls":
			kmsPullsSet = true
		}
	})
	if deprecatedPullsSet && !kmsPullsSet {
		finalMaxConcurrentSecretPulls = maxConcurrentSecretPulls
	}

	maxConcurrentKmsSecretPulls = finalMaxConcurrentSecretPulls

	ctrl.SetLogger(zap.New())

	printVersion()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := &backend.ProviderOptions{
		Region:           region,
		KmsEndpoint:      kmsEndpoint,
		KmsMaxConcurrent: maxConcurrentKmsSecretPulls,
		OosMaxConcurrent: maxConcurrentOosSecretPulls,
		ClusterId:        clusterId,
		Uid:              uid,
	}
	for providerName, f := range backend.SupportProvider {
		log.Info("new provider", "provider", providerName)
		f(opts)
	}

	var syncPeriod = 10 * time.Hour
	if disablePolling {
		syncPeriod = 365 * 24 * time.Hour
	}

	// Parse the namespace scope flags BEFORE creating the manager: the include
	// whitelist feeds cache.Options.DefaultNamespaces, fixed at NewManager time.
	nsSlice := func(ns string) []string {
		parts := strings.Split(strings.Trim(strings.TrimSpace(ns), "\""), ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			// Trim every element so whitespace variants cannot bypass the
			// same-name conflict fail-fast; drop entries from trailing commas.
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	watchNs := make(map[string]bool)
	if len(watchNamespaces) > 0 {
		parsedWatch := nsSlice(watchNamespaces)
		if len(parsedWatch) == 0 {
			// Fail-open is preserved, but the silent no-op must not go
			// unnoticed: the operator keeps running cluster-wide while the
			// configurator believes a scope is active.
			log.Info("WARNING: --watch-namespaces input contains no usable namespace after parsing; the flag is ignored and the scope stays cluster-wide",
				"input", watchNamespaces)
		}
		for _, ns := range parsedWatch {
			watchNs[ns] = true
		}
	}
	// Fail-fast on conflicts: a namespace listed in BOTH flags would let the
	// exclude entry silently overwrite the include entry and degrade the
	// whitelist into a near-cluster-wide watch, so refuse to start.
	var conflictingNs []string
	if len(excludeNamespaces) > 0 {
		parsedExclude := nsSlice(excludeNamespaces)
		if len(parsedExclude) == 0 {
			// Same silent no-op hazard as --watch-namespaces above.
			log.Info("WARNING: --exclude-namespaces input contains no usable namespace after parsing; the flag is ignored and the scope stays cluster-wide",
				"input", excludeNamespaces)
		}
		for _, ns := range parsedExclude {
			if watchNs[ns] {
				conflictingNs = append(conflictingNs, ns)
			}
			watchNs[ns] = false
		}
	}
	if len(conflictingNs) > 0 {
		sort.Strings(conflictingNs)
		log.Error(fmt.Errorf("namespace(s) %s are listed in both --watch-namespaces and --exclude-namespaces",
			strings.Join(conflictingNs, ", ")), "conflicting namespace configuration, refusing to start")
		os.Exit(1)
	}

	// Community-convention namespace scoping: once any namespace scope is
	// configured, the cluster-scoped controllers are auto-disabled (overrides
	// explicit flags; original values disclosed in the log) -- a
	// namespace-scoped operator must not provision cluster-wide resources.
	if scoped := len(watchNs) > 0; scoped && (processClusterSecretStore || processClusterExternalSecret) {
		log.Info("namespace scope configured via watch/exclude namespaces: cluster-scoped controllers auto-disabled; explicitly configured flag values have been overridden",
			"processClusterSecretStore", processClusterSecretStore,
			"processClusterExternalSecret", processClusterExternalSecret)
		processClusterSecretStore = false
		processClusterExternalSecret = false
	}

	// Include whitelist narrows the manager cache via DefaultNamespaces;
	// exclude entries are enforced at the predicate level, and the fail-fast
	// above guarantees both sets are disjoint.
	cacheOptions := cache.Options{
		SyncPeriod: &syncPeriod,
	}
	var whitelisted []string
	for ns, included := range watchNs {
		if included {
			whitelisted = append(whitelisted, ns)
		}
	}
	if len(whitelisted) > 0 {
		sort.Strings(whitelisted)
		cacheOptions.DefaultNamespaces = make(map[string]cache.Config, len(whitelisted))
		for _, ns := range whitelisted {
			cacheOptions.DefaultNamespaces[ns] = cache.Config{}
		}
		log.Info("manager cache restricted to the watched namespace whitelist",
			"namespaces", strings.Join(whitelisted, ", "))
	}

	if os.Getenv("POD_NAMESPACE") == "" {
		klog.Warningf("POD_NAMESPACE is not set: leader election namespace falls back to the in-cluster ServiceAccount namespace file; running out-of-cluster will fail manager startup")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                        scheme,
		Cache:                         cacheOptions,
		LeaderElection:                true,
		LeaderElectionID:              "ack-secret-manager-lock",
		LeaderElectionNamespace:       os.Getenv("POD_NAMESPACE"),
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "failed to add apis to scheme")
		os.Exit(1)
	}

	esReconciler := externalsecret.ExternalSecretReconciler{
		Client:                 mgr.GetClient(),
		APIReader:              mgr.GetAPIReader(),
		Log:                    ctrl.Log.WithName("controllers").WithName("ExternalSecret"),
		Ctx:                    ctx,
		DisablePolling:         disablePolling,
		CleanUpSecretOnFailure: cleanUpSecretOnFailure,
		ReconciliationPeriod:   reconcilePeriod,
		WatchNamespaces:        watchNs,
		RotationInterval:       rotationInterval,
		EnableCrossNamespace:   enableCrossNamespaceSecretStore,
		// Drives the freshness-guard degraded mode and the CSS-disabled status
		// notice when the CSS controller is not registered.
		ProcessClusterSecretStore: processClusterSecretStore,
	}

	esReconciler.KmsLimiter.SecretPullLimiter = rate.NewLimiter(rate.Limit(maxConcurrentKmsSecretPulls), 1)
	esReconciler.OosLimiter.SecretPullLimiter = rate.NewLimiter(rate.Limit(maxConcurrentOosSecretPulls), 1)
	err = (&esReconciler).SetupWithManager(mgr, reconcileCount)
	if err != nil {
		log.Error(err, "unable to create controller", "controller", "ExternalSecret")
		os.Exit(1)
	}

	scReconciler := secretstore.SecretStoreReconciler{
		CommonReconciler: &secretstore.CommonReconciler{
			Client:                      mgr.GetClient(),
			EnableCrossNamespaceAuthRef: enableCrossNamespaceAuthRef,
		},
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		Log:                  ctrl.Log.WithName("controllers").WithName("SecretStore"),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
	}
	if err = (&scReconciler).SetupWithManager(mgr, reconcileCount); err != nil {
		log.Error(err, "unable to create controller", "controller", "SecretStore")
		os.Exit(1)
	}

	// Setup ClusterSecretStore controller if enabled
	if processClusterSecretStore {
		cssReconciler := secretstore.ClusterSecretStoreReconciler{
			CommonReconciler: &secretstore.CommonReconciler{
				Client:                      mgr.GetClient(),
				EnableCrossNamespaceAuthRef: enableCrossNamespaceAuthRef,
			},
			Client:               mgr.GetClient(),
			Scheme:               mgr.GetScheme(),
			Log:                  ctrl.Log.WithName("controllers").WithName("ClusterSecretStore"),
			Ctx:                  ctx,
			ReconciliationPeriod: reconcilePeriod,
		}
		if err = (&cssReconciler).SetupWithManager(mgr, reconcileCount); err != nil {
			log.Error(err, "unable to create controller", "controller", "ClusterSecretStore")
			os.Exit(1)
		}
		log.Info("ClusterSecretStore controller started")
	} else {
		log.Info("ClusterSecretStore controller disabled")
	}

	// Setup ClusterExternalSecret controller if enabled
	if processClusterExternalSecret {
		cesReconciler := clusterexternalsecret.ClusterExternalSecretReconciler{
			Client:               mgr.GetClient(),
			Scheme:               mgr.GetScheme(),
			Log:                  ctrl.Log.WithName("controllers").WithName("ClusterExternalSecret"),
			Ctx:                  ctx,
			ReconciliationPeriod: reconcilePeriod,
			RotationInterval:     rotationInterval,
			DisablePolling:       disablePolling,
			EnableCrossNamespace: enableCrossNamespaceSecretStore,
		}
		if err = (&cesReconciler).SetupWithManager(mgr, reconcileCount); err != nil {
			log.Error(err, "unable to create controller", "controller", "ClusterExternalSecret")
			os.Exit(1)
		}
		log.Info("ClusterExternalSecret controller started")
	} else {
		log.Info("ClusterExternalSecret controller disabled")
	}

	// Watches Secret data changes to trigger re-sync
	secretRefReconciler := &secret.SecretReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("SecretRef"),
		ProcessClusterSecretStore: processClusterSecretStore,
	}
	if err = secretRefReconciler.SetupWithManager(mgr, reconcileCount); err != nil {
		log.Error(err, "unable to create controller", "controller", "SecretRef")
		os.Exit(1)
	}
	log.Info("SecretRef controller started")

	// Watches ServiceAccount annotation changes to rebuild auth clients
	serviceAccountRefReconciler := &serviceaccount.ServiceAccountReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("ServiceAccountRef"),
		ProcessClusterSecretStore: processClusterSecretStore,
	}
	if err = serviceAccountRefReconciler.SetupWithManager(mgr, reconcileCount); err != nil {
		log.Error(err, "unable to create controller", "controller", "ServiceAccountRef")
		os.Exit(1)
	}
	log.Info("ServiceAccountRef controller started")

	log.Info("starting ack-secret-manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "failed to run manager")
		os.Exit(1)
	}
}
