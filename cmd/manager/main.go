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
	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller"
	"github.com/AliyunContainerService/ack-secret-manager/version"
	"github.com/operator-framework/operator-lib/leader"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"os"
	"runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"strings"
	"time"
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
}

func main() {
	var reconcilePeriod time.Duration
	var rotationInterval time.Duration
	var reconcileCount int
	var disablePolling bool
	var selectedBackend string
	var watchNamespaces string
	var excludeNamespaces string

	backendCfg := backend.Config{}

	flag.StringVar(&selectedBackend, "backend", "alicloud-kms", "Selected backend. Only alicloud-kms supported")
	flag.DurationVar(&rotationInterval, "polling-interval", 120*time.Second, "How often the controller will sync existing secret from kms")
	flag.BoolVar(&disablePolling, "disable-polling", false, "Disable auto polling external secret from kms.")
	flag.DurationVar(&backendCfg.TokenRotationPeriod, "token-rotation-period", 120*time.Second, "Polling interval to check token expiration time.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue externalsecret events")
	flag.IntVar(&reconcileCount, "reconcile-count", 1, "The max concurrency reconcile work at the same time")
	flag.StringVar(&backendCfg.Region, "region", "", "Region id, change it according to where you want to pull the secret from")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that ack-secret-manager watch. By default all namespaces are watched.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces that that ack-secret-manager will not watch. By default all namespaces are watched.")
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

	backendClient, err := backend.NewBackendClient(ctx, selectedBackend, backendCfg)
	if err != nil {
		log.Error(err, "could not build backend client")
		os.Exit(1)
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
	log.Info("backendClient is:", "backendClient", &backendClient)
	esReconciler := controller.ExternalSecretReconciler{
		Backend:              *backendClient,
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Log:                  ctrl.Log.WithName("controllers").WithName("ExternalSecret"),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
		WatchNamespaces:      watchNs,
		RotationInterval:     rotationInterval,
	}
	err = (&esReconciler).SetupWithManager(mgr, reconcileCount)
	if err != nil {
		log.Error(err, "unable to create controller", "controller", "ExternalSecret")
		os.Exit(1)
	}

	//not start auto sync job if disable polling
	if !disablePolling {
		esReconciler.InitSecretStore()
		go esReconciler.SecretRotationJob()
	}

	log.Info("starting ack-secret-manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "failed to run manager")
		os.Exit(1)
	}
}
