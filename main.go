/*
Copyright 2020 The Flux authors

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
	"fmt"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/azure"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcfg "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling/clusterreader"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling/engine"
	"github.com/fluxcd/pkg/runtime/acl"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	runtimeCtrl "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"
	feathelper "github.com/fluxcd/pkg/runtime/features"
	"github.com/fluxcd/pkg/runtime/jitter"
	"github.com/fluxcd/pkg/runtime/leaderelection"
	"github.com/fluxcd/pkg/runtime/logger"
	"github.com/fluxcd/pkg/runtime/metrics"
	"github.com/fluxcd/pkg/runtime/pprof"
	"github.com/fluxcd/pkg/runtime/probes"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/kustomize-controller/internal/controller"
	"github.com/fluxcd/kustomize-controller/internal/features"
	// +kubebuilder:scaffold:imports
)

const controllerName = "kustomize-controller"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = sourcev1.AddToScheme(scheme)
	_ = sourcev1b2.AddToScheme(scheme)
	_ = kustomizev1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		metricsAddr             string
		eventsAddr              string
		healthAddr              string
		concurrent              int
		concurrentSSA           int
		requeueDependency       time.Duration
		clientOptions           runtimeClient.Options
		kubeConfigOpts          runtimeClient.KubeConfigOptions
		logOptions              logger.Options
		leaderElectionOptions   leaderelection.Options
		rateLimiterOptions      runtimeCtrl.RateLimiterOptions
		watchOptions            runtimeCtrl.WatchOptions
		intervalJitterOptions   jitter.IntervalOptions
		aclOptions              acl.Options
		noRemoteBases           bool
		httpRetry               int
		defaultServiceAccount   string
		featureGates            feathelper.FeatureGates
		disallowedFieldManagers []string
	)

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&eventsAddr, "events-addr", "", "The address of the events receiver.")
	flag.StringVar(&healthAddr, "health-addr", ":9440", "The address the health endpoint binds to.")
	flag.IntVar(&concurrent, "concurrent", 4, "The number of concurrent kustomize reconciles.")
	flag.IntVar(&concurrentSSA, "concurrent-ssa", 4, "The number of concurrent server-side apply operations.")
	flag.DurationVar(&requeueDependency, "requeue-dependency", 30*time.Second, "The interval at which failing dependencies are reevaluated.")
	flag.BoolVar(&noRemoteBases, "no-remote-bases", false,
		"Disallow remote bases usage in Kustomize overlays. When this flag is enabled, all resources must refer to local files included in the source artifact.")
	flag.IntVar(&httpRetry, "http-retry", 9, "The maximum number of retries when failing to fetch artifacts over HTTP.")
	flag.StringVar(&defaultServiceAccount, "default-service-account", "", "Default service account used for impersonation.")
	flag.StringArrayVar(&disallowedFieldManagers, "override-manager", []string{}, "Field manager disallowed to perform changes on managed resources.")

	clientOptions.BindFlags(flag.CommandLine)
	logOptions.BindFlags(flag.CommandLine)
	leaderElectionOptions.BindFlags(flag.CommandLine)
	aclOptions.BindFlags(flag.CommandLine)
	kubeConfigOpts.BindFlags(flag.CommandLine)
	rateLimiterOptions.BindFlags(flag.CommandLine)
	featureGates.BindFlags(flag.CommandLine)
	watchOptions.BindFlags(flag.CommandLine)
	intervalJitterOptions.BindFlags(flag.CommandLine)

	flag.Parse()

	logger.SetLogger(logger.NewLogger(logOptions))

	ctx := ctrl.SetupSignalHandler()

	if err := featureGates.WithLogger(setupLog).SupportedFeatures(features.FeatureGates()); err != nil {
		setupLog.Error(err, "unable to load feature gates")
		os.Exit(1)
	}

	if err := intervalJitterOptions.SetGlobalJitter(nil); err != nil {
		setupLog.Error(err, "unable to set global jitter")
		os.Exit(1)
	}

	watchNamespace := ""
	if !watchOptions.AllNamespaces {
		watchNamespace = os.Getenv("RUNTIME_NAMESPACE")
	}

	watchSelector, err := runtimeCtrl.GetWatchSelector(watchOptions)
	if err != nil {
		setupLog.Error(err, "unable to configure watch label selector for manager")
		os.Exit(1)
	}

	var disableCacheFor []ctrlclient.Object
	shouldCache, err := features.Enabled(features.CacheSecretsAndConfigMaps)
	if err != nil {
		setupLog.Error(err, "unable to check feature gate "+features.CacheSecretsAndConfigMaps)
		os.Exit(1)
	}
	if !shouldCache {
		disableCacheFor = append(disableCacheFor, &corev1.Secret{}, &corev1.ConfigMap{})
	}

	leaderElectionId := fmt.Sprintf("%s-%s", controllerName, "leader-election")
	if watchOptions.LabelSelector != "" {
		leaderElectionId = leaderelection.GenerateID(leaderElectionId, watchOptions.LabelSelector)
	}

	restConfig := runtimeClient.GetConfigOrDie(clientOptions)
	mgrConfig := ctrl.Options{
		Scheme:                        scheme,
		HealthProbeBindAddress:        healthAddr,
		LeaderElection:                leaderElectionOptions.Enable,
		LeaderElectionReleaseOnCancel: leaderElectionOptions.ReleaseOnCancel,
		LeaseDuration:                 &leaderElectionOptions.LeaseDuration,
		RenewDeadline:                 &leaderElectionOptions.RenewDeadline,
		RetryPeriod:                   &leaderElectionOptions.RetryPeriod,
		LeaderElectionID:              leaderElectionId,
		Logger:                        ctrl.Log,
		Client: ctrlclient.Options{
			Cache: &ctrlclient.CacheOptions{
				DisableFor: disableCacheFor,
			},
		},
		Cache: ctrlcache.Options{
			ByObject: map[ctrlclient.Object]ctrlcache.ByObject{
				&kustomizev1.Kustomization{}: {Label: watchSelector},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			ExtraHandlers: pprof.GetHandlers(),
		},
		Controller: ctrlcfg.Controller{
			MaxConcurrentReconciles: concurrent,
			RecoverPanic:            ptr.To(true),
		},
	}

	if watchNamespace != "" {
		mgrConfig.Cache.DefaultNamespaces = map[string]ctrlcache.Config{
			watchNamespace: ctrlcache.Config{},
		}
	}

	mgr, err := ctrl.NewManager(restConfig, mgrConfig)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	probes.SetupChecks(mgr, setupLog)

	var eventRecorder *events.Recorder
	if eventRecorder, err = events.NewRecorder(mgr, ctrl.Log, eventsAddr, controllerName); err != nil {
		setupLog.Error(err, "unable to create event recorder")
		os.Exit(1)
	}

	metricsH := runtimeCtrl.NewMetrics(mgr, metrics.MustMakeRecorder(), kustomizev1.KustomizationFinalizer)

	restMapper, err := runtimeClient.NewDynamicRESTMapper(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create REST mapper")
		os.Exit(1)
	}

	var clusterReader engine.ClusterReaderFactory
	if ok, _ := features.Enabled(features.DisableStatusPollerCache); ok {
		clusterReader = engine.ClusterReaderFactoryFunc(clusterreader.NewDirectClusterReader)
	}

	failFast := true
	if ok, _ := features.Enabled(features.DisableFailFastBehavior); ok {
		failFast = false
	}

	strictSubstitutions, err := features.Enabled(features.StrictPostBuildSubstitutions)
	if err != nil {
		setupLog.Error(err, "unable to check feature gate "+features.StrictPostBuildSubstitutions)
		os.Exit(1)
	}

	groupChangeLog, err := features.Enabled(features.GroupChangeLog)
	if err != nil {
		setupLog.Error(err, "unable to check feature gate "+features.GroupChangeLog)
		os.Exit(1)
	}

	enableDependencyQueueing, err := features.Enabled(features.EnableDependencyQueueing)
	if err != nil {
		setupLog.Error(err, "unable to check feature gate "+features.EnableDependencyQueueing)
		os.Exit(1)
	}

	if err = (&controller.KustomizationReconciler{
		ControllerName:          controllerName,
		DefaultServiceAccount:   defaultServiceAccount,
		Client:                  mgr.GetClient(),
		Mapper:                  restMapper,
		APIReader:               mgr.GetAPIReader(),
		Metrics:                 metricsH,
		EventRecorder:           eventRecorder,
		NoCrossNamespaceRefs:    aclOptions.NoCrossNamespaceRefs,
		NoRemoteBases:           noRemoteBases,
		FailFast:                failFast,
		ConcurrentSSA:           concurrentSSA,
		KubeConfigOpts:          kubeConfigOpts,
		ClusterReader:           clusterReader,
		DisallowedFieldManagers: disallowedFieldManagers,
		StrictSubstitutions:     strictSubstitutions,
		GroupChangeLog:          groupChangeLog,
	}).SetupWithManager(ctx, mgr, controller.KustomizationReconcilerOptions{
		DependencyRequeueInterval: requeueDependency,
		EnableDependencyQueueing:  enableDependencyQueueing,
		HTTPRetry:                 httpRetry,
		RateLimiter:               runtimeCtrl.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", controllerName)
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
