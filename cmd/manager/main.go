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
	"golang.org/x/sync/semaphore"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/operator-framework/operator-lib/leader"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	_ "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/kms"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/externalsecret"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/secretstore"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
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
	var tokenRotationPeriod time.Duration
	var maxConcurrentSecretPulls int

	flag.StringVar(&selectedBackend, "backend", "alicloud-kms", "Selected backend. Only alicloud-kms supported")
	flag.DurationVar(&rotationInterval, "polling-interval", 120*time.Second, "How often the controller will sync existing secret from kms")
	flag.BoolVar(&disablePolling, "disable-polling", false, "Disable auto polling external secret from kms.")
	flag.DurationVar(&tokenRotationPeriod, "token-rotation-period", 120*time.Second, "Polling interval to check token expiration time.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue externalsecret events")
	flag.IntVar(&reconcileCount, "reconcile-count", 1, "The max concurrency reconcile work at the same time")
	flag.StringVar(&region, "region", "", "Region id, change it according to where you want to pull the secret from")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that ack-secret-manager watch. By default all namespaces are watched.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces that that ack-secret-manager will not watch. By default all namespaces are watched.")
	flag.IntVar(&maxConcurrentSecretPulls, "max-concurrent-secret-pulls", 5, "used to control how many secrets are pulled at the same time.\n\n")

	flag.Parse()

	ctrl.SetLogger(zap.New())

	printVersion()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Become the leader before proceeding
	// Using leader-for-life selection to avoid split brain
	err := leader.Become(ctx, "ack-secret-manager-lock")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	instanceRegion, err := utils.GetRegion()
	if err != nil {
		log.Error(err, "get region failed")
	}
	if region == "" || region != instanceRegion {
		region = instanceRegion
	}
	opts := &backend.ProviderOptions{
		Region:        region,
		MaxConcurrent: maxConcurrentSecretPulls,
	}
	for providerName, f := range backend.SupportProvider {
		log.Info("new provider ", providerName)
		f(opts)
	}

	err = backend.NewProviderClientByENV(ctx, region)
	if err != nil {
		log.Error(err, "")
	}
	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

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
	w := semaphore.NewWeighted(int64(opts.MaxConcurrent))
	esReconciler := externalsecret.ExternalSecretReconciler{
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Log:                  ctrl.Log.WithName("controllers").WithName("ExternalSecret"),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
		WatchNamespaces:      watchNs,
		RotationInterval:     rotationInterval,
		ConcurrentController: w,
	}
	err = (&esReconciler).SetupWithManager(mgr, reconcileCount)
	if err != nil {
		log.Error(err, "unable to create controller", "controller", "ExternalSecret")
		os.Exit(1)
	}
	scReconciler := secretstore.SecretStoreReconciler{
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
	//not start auto sync job if disable polling
	if !disablePolling {
		err := esReconciler.InitSecretCache()
		if err != nil {
			log.Error(err, "failed to init secret cache")
			os.Exit(1)
		}
		go esReconciler.SecretRotationJob()
	}

	log.Info("starting ack-secret-manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "failed to run manager")
		os.Exit(1)
	}
}
