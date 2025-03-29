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

package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/fluxcd/pkg/ssa/normalize"
	ssautil "github.com/fluxcd/pkg/ssa/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling/engine"
	"github.com/fluxcd/cli-utils/pkg/object"
	apiacl "github.com/fluxcd/pkg/apis/acl"
	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/http/fetch"
	generator "github.com/fluxcd/pkg/kustomize"
	"github.com/fluxcd/pkg/runtime/acl"
	"github.com/fluxcd/pkg/runtime/cel"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/conditions"
	runtimeCtrl "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/jitter"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/kustomize-controller/internal/decryptor"
	"github.com/fluxcd/kustomize-controller/internal/inventory"
)

// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets;ocirepositories;gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets/status;ocirepositories/status;gitrepositories/status,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	kuberecorder.EventRecorder
	runtimeCtrl.Metrics

	artifactFetchRetries int
	requeueDependency    time.Duration

	Mapper                  apimeta.RESTMapper
	APIReader               client.Reader
	ClusterReader           engine.ClusterReaderFactory
	ControllerName          string
	statusManager           string
	NoCrossNamespaceRefs    bool
	NoRemoteBases           bool
	FailFast                bool
	DefaultServiceAccount   string
	KubeConfigOpts          runtimeClient.KubeConfigOptions
	ConcurrentSSA           int
	DisallowedFieldManagers []string
	StrictSubstitutions     bool
	GroupChangeLog          bool
}

// KustomizationReconcilerOptions contains options for the KustomizationReconciler.
type KustomizationReconcilerOptions struct {
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
	EnableDependencyQueueing  bool
	RateLimiter               workqueue.TypedRateLimiter[reconcile.Request]
}

func (r *KustomizationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	const (
		ociRepositoryIndexKey string = ".metadata.ociRepository"
		gitRepositoryIndexKey string = ".metadata.gitRepository"
		bucketIndexKey        string = ".metadata.bucket"
	)

	// Index the Kustomizations by the OCIRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, ociRepositoryIndexKey,
		r.indexBy(sourcev1b2.OCIRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Kustomizations by the GitRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, gitRepositoryIndexKey,
		r.indexBy(sourcev1.GitRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Kustomizations by the Bucket references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, bucketIndexKey,
		r.indexBy(sourcev1.BucketKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	r.requeueDependency = opts.DependencyRequeueInterval
	r.statusManager = fmt.Sprintf("gotk-%s", r.ControllerName)
	r.artifactFetchRetries = opts.HTTPRetry

	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&sourcev1b2.OCIRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(ociRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(gitRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1.Bucket{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(bucketIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		)

	if opts.EnableDependencyQueueing {
		// Index the Kustomizations by the dependsOn references they (may) point at.
		if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, dependsOnIndexKey,
			r.indexDependsOn); err != nil {
			return fmt.Errorf("failed setting index fields: %w", err)
		}

		controllerBuilder = controllerBuilder.
			Watches(
				&kustomizev1.Kustomization{},
				handler.EnqueueRequestsFromMapFunc(r.requestsForDependents),
				builder.WithPredicates(KustomizationReadyChangePredicate{}),
			)
	}

	return controllerBuilder.
		WithOptions(controller.Options{
			RateLimiter: opts.RateLimiter,
		}).
		Complete(r)
}

func (r *KustomizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)
	reconcileStart := time.Now()

	obj := &kustomizev1.Kustomization{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Initialize the runtime patcher with the current version of the object.
	patcher := patch.NewSerialPatcher(obj, r.Client)

	// Finalise the reconciliation and report the results.
	defer func() {
		// Patch finalizers, status and conditions.
		if err := r.finalizeStatus(ctx, obj, patcher); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}

		// Record Prometheus metrics.
		r.Metrics.RecordDuration(ctx, obj, reconcileStart)

		// Log and emit success event.
		if conditions.IsReady(obj) {
			msg := fmt.Sprintf("Reconciliation finished in %s, next run in %s",
				time.Since(reconcileStart).String(),
				obj.Spec.Interval.Duration.String())
			log.Info(msg, "revision", obj.Status.LastAttemptedRevision)
			r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityInfo, msg,
				map[string]string{
					kustomizev1.GroupVersion.Group + "/" + eventv1.MetaCommitStatusKey: eventv1.MetaCommitStatusUpdateValue,
				})
		}
	}()

	// Prune managed resources if the object is under deletion.
	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, obj)
	}

	// Add finalizer first if it doesn't exist to avoid the race condition
	// between init and delete.
	// Note: Finalizers in general can only be added when the deletionTimestamp
	// is not set.
	if !controllerutil.ContainsFinalizer(obj, kustomizev1.KustomizationFinalizer) {
		controllerutil.AddFinalizer(obj, kustomizev1.KustomizationFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip reconciliation if the object is suspended.
	if obj.Spec.Suspend {
		log.Info("Reconciliation is suspended for this object")
		return ctrl.Result{}, nil
	}

	// Configure custom health checks.
	statusReaders, err := cel.PollerWithCustomHealthChecks(ctx, obj.Spec.HealthCheckExprs)
	if err != nil {
		const msg = "Reconciliation failed terminally due to configuration error"
		errMsg := fmt.Sprintf("%s: %v", msg, err)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.InvalidCELExpressionReason, "%s", errMsg)
		conditions.MarkStalled(obj, meta.InvalidCELExpressionReason, "%s", errMsg)
		obj.Status.ObservedGeneration = obj.Generation
		log.Error(err, msg)
		r.event(obj, "", "", eventv1.EventSeverityError, errMsg, nil)
		return ctrl.Result{}, nil
	}

	// Resolve the source reference and requeue the reconciliation if the source is not found.
	artifactSource, err := r.getSource(ctx, obj)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)

		if apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Source '%s' not found", obj.Spec.SourceRef.String())
			log.Info(msg)
			return ctrl.Result{RequeueAfter: obj.GetRetryInterval()}, nil
		}

		if acl.IsAccessDenied(err) {
			conditions.MarkFalse(obj, meta.ReadyCondition, apiacl.AccessDeniedReason, "%s", err)
			log.Error(err, "Access denied to cross-namespace source")
			r.event(obj, "", "", eventv1.EventSeverityError, err.Error(), nil)
			return ctrl.Result{RequeueAfter: obj.GetRetryInterval()}, nil
		}

		// Retry with backoff on transient errors.
		return ctrl.Result{}, err
	}

	// Requeue the reconciliation if the source artifact is not found.
	if artifactSource.GetArtifact() == nil {
		msg := fmt.Sprintf("Source artifact not found, retrying in %s", r.requeueDependency.String())
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", msg)
		log.Info(msg)
		return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
	}
	revision := artifactSource.GetArtifact().Revision
	originRevision := getOriginRevision(artifactSource)

	// Check dependencies and requeue the reconciliation if the check fails.
	if len(obj.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(ctx, obj, artifactSource); err != nil {
			conditions.MarkFalse(obj, meta.ReadyCondition, meta.DependencyNotReadyReason, "%s", err)
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			log.Info(msg, "err", err)
			r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, msg, nil)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}
		log.Info("All dependencies are ready, proceeding with reconciliation")
	}

	// Reconcile the latest revision.
	reconcileErr := r.reconcile(ctx, obj, artifactSource, patcher, statusReaders)

	// Requeue at the specified retry interval if the artifact tarball is not found.
	if errors.Is(reconcileErr, fetch.ErrFileNotFound) {
		msg := fmt.Sprintf("Source is not ready, artifact not found, retrying in %s", r.requeueDependency.String())
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", msg)
		log.Info(msg)
		return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
	}

	// Broadcast the reconciliation failure and requeue at the specified retry interval.
	if reconcileErr != nil {
		log.Error(reconcileErr, fmt.Sprintf("Reconciliation failed after %s, next try in %s",
			time.Since(reconcileStart).String(),
			obj.GetRetryInterval().String()),
			"revision",
			revision)
		r.event(obj, revision, originRevision, eventv1.EventSeverityError,
			reconcileErr.Error(), nil)
		return ctrl.Result{RequeueAfter: obj.GetRetryInterval()}, nil
	}

	// Requeue the reconciliation at the specified interval.
	return ctrl.Result{RequeueAfter: jitter.JitteredIntervalDuration(obj.GetRequeueAfter())}, nil
}

func (r *KustomizationReconciler) reconcile(
	ctx context.Context,
	obj *kustomizev1.Kustomization,
	src sourcev1.Source,
	patcher *patch.SerialPatcher,
	statusReaders []func(apimeta.RESTMapper) engine.StatusReader) error {
	log := ctrl.LoggerFrom(ctx)

	// Update status with the reconciliation progress.
	revision := src.GetArtifact().Revision
	originRevision := getOriginRevision(src)
	progressingMsg := fmt.Sprintf("Fetching manifests for revision %s with a timeout of %s", revision, obj.GetTimeout().String())
	conditions.MarkUnknown(obj, meta.ReadyCondition, meta.ProgressingReason, "%s", "Reconciliation in progress")
	conditions.MarkReconciling(obj, meta.ProgressingReason, "%s", progressingMsg)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Create a snapshot of the current inventory.
	oldInventory := inventory.New()
	if obj.Status.Inventory != nil {
		obj.Status.Inventory.DeepCopyInto(oldInventory)
	}

	// Create tmp dir.
	tmpDir, err := MkdirTempAbs("", "kustomization-")
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		conditions.MarkFalse(obj, meta.ReadyCondition, sourcev1.DirCreationFailedReason, "%s", err)
		return err
	}

	defer func(path string) {
		if err := os.RemoveAll(path); err != nil {
			log.Error(err, "failed to remove tmp dir", "path", path)
		}
	}(tmpDir)

	// Download artifact and extract files to the tmp dir.
	if err = fetch.NewArchiveFetcherWithLogger(
		r.artifactFetchRetries,
		tar.UnlimitedUntarSize,
		tar.UnlimitedUntarSize,
		os.Getenv("SOURCE_CONTROLLER_LOCALHOST"),
		ctrl.LoggerFrom(ctx),
	).Fetch(src.GetArtifact().URL, src.GetArtifact().Digest, tmpDir); err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return err
	}

	// check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, obj.Spec.Path)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return err
	}

	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return err
	}

	// Report progress and set last attempted revision in status.
	obj.Status.LastAttemptedRevision = revision
	progressingMsg = fmt.Sprintf("Building manifests for revision %s with a timeout of %s", revision, obj.GetTimeout().String())
	conditions.MarkReconciling(obj, meta.ProgressingReason, "%s", progressingMsg)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Configure the Kubernetes client for impersonation.
	var impersonatorOpts []runtimeClient.ImpersonatorOption
	if r.DefaultServiceAccount != "" || obj.Spec.ServiceAccountName != "" {
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithServiceAccount(r.DefaultServiceAccount, obj.Spec.ServiceAccountName, obj.GetNamespace()))
	}
	if obj.Spec.KubeConfig != nil {
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithKubeConfig(obj.Spec.KubeConfig, r.KubeConfigOpts, obj.GetNamespace()))
	}
	if r.ClusterReader != nil || len(statusReaders) > 0 {
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithPolling(r.ClusterReader, statusReaders...))
	}
	impersonation := runtimeClient.NewImpersonator(r.Client, impersonatorOpts...)

	// Create the Kubernetes client that runs under impersonation.
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return fmt.Errorf("failed to build kube client: %w", err)
	}

	// Generate kustomization.yaml if needed.
	k, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.BuildFailedReason, "%s", err)
		return err
	}
	err = r.generate(unstructured.Unstructured{Object: k}, tmpDir, dirPath)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.BuildFailedReason, "%s", err)
		return err
	}

	// Build the Kustomize overlay and decrypt secrets if needed.
	resources, err := r.build(ctx, obj, unstructured.Unstructured{Object: k}, tmpDir, dirPath)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.BuildFailedReason, "%s", err)
		return err
	}

	// Convert the build result into Kubernetes unstructured objects.
	objects, err := ssautil.ReadObjects(bytes.NewReader(resources))
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.BuildFailedReason, "%s", err)
		return err
	}

	// Create the server-side apply manager.
	resourceManager := ssa.NewResourceManager(kubeClient, statusPoller, ssa.Owner{
		Field: r.ControllerName,
		Group: kustomizev1.GroupVersion.Group,
	})
	resourceManager.SetOwnerLabels(objects, obj.GetName(), obj.GetNamespace())
	resourceManager.SetConcurrency(r.ConcurrentSSA)

	// Update status with the reconciliation progress.
	progressingMsg = fmt.Sprintf("Detecting drift for revision %s with a timeout of %s", revision, obj.GetTimeout().String())
	conditions.MarkReconciling(obj, meta.ProgressingReason, "%s", progressingMsg)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Validate and apply resources in stages.
	drifted, changeSet, err := r.apply(ctx, resourceManager, obj, revision, originRevision, objects)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Create an inventory from the reconciled resources.
	newInventory := inventory.New()
	err = inventory.AddChangeSet(newInventory, changeSet)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Set last applied inventory in status.
	obj.Status.Inventory = newInventory

	// Detect stale resources which are subject to garbage collection.
	staleObjects, err := inventory.Diff(oldInventory, newInventory)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Run garbage collection for stale resources that do not have pruning disabled.
	if _, err := r.prune(ctx, resourceManager, obj, revision, originRevision, staleObjects); err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.PruneFailedReason, "%s", err)
		return err
	}

	// Run the health checks for the last applied resources.
	isNewRevision := !src.GetArtifact().HasRevision(obj.Status.LastAppliedRevision)
	if err := r.checkHealth(ctx,
		resourceManager,
		patcher,
		obj,
		revision,
		originRevision,
		isNewRevision,
		drifted,
		changeSet.ToObjMetadataSet()); err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.HealthCheckFailedReason, "%s", err)
		return err
	}

	// Set last applied revisions.
	obj.Status.LastAppliedRevision = revision
	obj.Status.LastAppliedOriginRevision = originRevision

	// Mark the object as ready.
	conditions.MarkTrue(obj,
		meta.ReadyCondition,
		meta.ReconciliationSucceededReason,
		"Applied revision: %s", revision)

	return nil
}

func (r *KustomizationReconciler) checkDependencies(ctx context.Context,
	obj *kustomizev1.Kustomization,
	source sourcev1.Source) error {
	for _, d := range obj.Spec.DependsOn {
		if d.Namespace == "" {
			d.Namespace = obj.GetNamespace()
		}
		dName := types.NamespacedName{
			Namespace: d.Namespace,
			Name:      d.Name,
		}
		var k kustomizev1.Kustomization
		err := r.APIReader.Get(ctx, dName, &k)
		if err != nil {
			return fmt.Errorf("dependency '%s' not found: %w", dName, err)
		}

		if len(k.Status.Conditions) == 0 || k.Generation != k.Status.ObservedGeneration {
			return fmt.Errorf("dependency '%s' is not ready", dName)
		}

		if !apimeta.IsStatusConditionTrue(k.Status.Conditions, meta.ReadyCondition) {
			return fmt.Errorf("dependency '%s' is not ready", dName)
		}

		srcNamespace := k.Spec.SourceRef.Namespace
		if srcNamespace == "" {
			srcNamespace = k.GetNamespace()
		}
		dSrcNamespace := obj.Spec.SourceRef.Namespace
		if dSrcNamespace == "" {
			dSrcNamespace = obj.GetNamespace()
		}

		if k.Spec.SourceRef.Name == obj.Spec.SourceRef.Name &&
			srcNamespace == dSrcNamespace &&
			k.Spec.SourceRef.Kind == obj.Spec.SourceRef.Kind &&
			!source.GetArtifact().HasRevision(k.Status.LastAppliedRevision) {
			return fmt.Errorf("dependency '%s' revision is not up to date", dName)
		}
	}

	return nil
}

func (r *KustomizationReconciler) getSource(ctx context.Context,
	obj *kustomizev1.Kustomization) (sourcev1.Source, error) {
	var src sourcev1.Source
	sourceNamespace := obj.GetNamespace()
	if obj.Spec.SourceRef.Namespace != "" {
		sourceNamespace = obj.Spec.SourceRef.Namespace
	}
	namespacedName := types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      obj.Spec.SourceRef.Name,
	}

	if r.NoCrossNamespaceRefs && sourceNamespace != obj.GetNamespace() {
		return src, acl.AccessDeniedError(
			fmt.Sprintf("can't access '%s/%s', cross-namespace references have been blocked",
				obj.Spec.SourceRef.Kind, namespacedName))
	}

	switch obj.Spec.SourceRef.Kind {
	case sourcev1b2.OCIRepositoryKind:
		var repository sourcev1b2.OCIRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1.BucketKind:
		var bucket sourcev1.Bucket
		err := r.Client.Get(ctx, namespacedName, &bucket)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &bucket
	default:
		return src, fmt.Errorf("source `%s` kind '%s' not supported",
			obj.Spec.SourceRef.Name, obj.Spec.SourceRef.Kind)
	}
	return src, nil
}

func (r *KustomizationReconciler) generate(obj unstructured.Unstructured,
	workDir string, dirPath string) error {
	_, err := generator.NewGenerator(workDir, obj).WriteFile(dirPath)
	return err
}

func (r *KustomizationReconciler) build(ctx context.Context,
	obj *kustomizev1.Kustomization, u unstructured.Unstructured,
	workDir, dirPath string) ([]byte, error) {
	dec, cleanup, err := decryptor.NewTempDecryptor(workDir, r.Client, obj)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Import decryption keys
	if err := dec.ImportKeys(ctx); err != nil {
		return nil, err
	}

	// Decrypt Kustomize EnvSources files before build
	if err = dec.DecryptSources(dirPath); err != nil {
		return nil, fmt.Errorf("error decrypting sources: %w", err)
	}

	m, err := generator.SecureBuild(workDir, dirPath, !r.NoRemoteBases)
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed: %w", err)
	}

	for _, res := range m.Resources() {
		// check if resources conform to the Kubernetes API conventions
		if res.GetName() == "" || res.GetKind() == "" || res.GetApiVersion() == "" {
			return nil, fmt.Errorf("failed to decode Kubernetes apiVersion, kind and name from: %v", res.String())
		}

		// check if resources are encrypted and decrypt them before generating the final YAML
		if obj.Spec.Decryption != nil {
			outRes, err := dec.DecryptResource(res)
			if err != nil {
				return nil, fmt.Errorf("decryption failed for '%s': %w", res.GetName(), err)
			}

			if outRes != nil {
				_, err = m.Replace(res)
				if err != nil {
					return nil, err
				}
			}
		}

		// run variable substitutions
		if obj.Spec.PostBuild != nil {
			outRes, err := generator.SubstituteVariables(ctx, r.Client, u, res,
				generator.SubstituteWithStrict(r.StrictSubstitutions))
			if err != nil {
				return nil, fmt.Errorf("post build failed for '%s': %w", res.GetName(), err)
			}

			if outRes != nil {
				_, err = m.Replace(res)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	resources, err := m.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed: %w", err)
	}

	return resources, nil
}

func (r *KustomizationReconciler) apply(ctx context.Context,
	manager *ssa.ResourceManager,
	obj *kustomizev1.Kustomization,
	revision string,
	originRevision string,
	objects []*unstructured.Unstructured) (bool, *ssa.ChangeSet, error) {
	log := ctrl.LoggerFrom(ctx)

	if err := normalize.UnstructuredList(objects); err != nil {
		return false, nil, err
	}

	if cmeta := obj.Spec.CommonMetadata; cmeta != nil {
		ssautil.SetCommonMetadata(objects, cmeta.Labels, cmeta.Annotations)
	}

	applyOpts := ssa.DefaultApplyOptions()
	applyOpts.Force = obj.Spec.Force
	applyOpts.ExclusionSelector = map[string]string{
		fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
		fmt.Sprintf("%s/ssa", kustomizev1.GroupVersion.Group):       kustomizev1.IgnoreValue,
	}
	applyOpts.IfNotPresentSelector = map[string]string{
		fmt.Sprintf("%s/ssa", kustomizev1.GroupVersion.Group): kustomizev1.IfNotPresentValue,
	}
	applyOpts.ForceSelector = map[string]string{
		fmt.Sprintf("%s/force", kustomizev1.GroupVersion.Group): kustomizev1.EnabledValue,
	}

	fieldManagers := []ssa.FieldManager{
		{
			// to undo changes made with 'kubectl apply --server-side --force-conflicts'
			Name:          "kubectl",
			OperationType: metav1.ManagedFieldsOperationApply,
		},
		{
			// to undo changes made with 'kubectl apply'
			Name:          "kubectl",
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
		{
			// to undo changes made with 'kubectl apply'
			Name:          "before-first-apply",
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
		{
			// to undo changes made by the controller before SSA
			Name:          r.ControllerName,
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
	}

	for _, fieldManager := range r.DisallowedFieldManagers {
		fieldManagers = append(fieldManagers, ssa.FieldManager{
			Name:          fieldManager,
			OperationType: metav1.ManagedFieldsOperationApply,
		})
		// to undo changes made by the controller before SSA
		fieldManagers = append(fieldManagers, ssa.FieldManager{
			Name:          fieldManager,
			OperationType: metav1.ManagedFieldsOperationUpdate,
		})
	}

	applyOpts.Cleanup = ssa.ApplyCleanupOptions{
		Annotations: []string{
			// remove the kubectl annotation
			corev1.LastAppliedConfigAnnotation,
			// remove deprecated fluxcd.io annotations
			"kustomize.toolkit.fluxcd.io/checksum",
			"fluxcd.io/sync-checksum",
		},
		Labels: []string{
			// remove deprecated fluxcd.io labels
			"fluxcd.io/sync-gc-mark",
		},
		FieldManagers: fieldManagers,
		Exclusions: map[string]string{
			fmt.Sprintf("%s/ssa", kustomizev1.GroupVersion.Group): kustomizev1.MergeValue,
		},
	}

	// contains only CRDs and Namespaces
	var defStage []*unstructured.Unstructured

	// contains only Kubernetes Class types e.g.: RuntimeClass, PriorityClass,
	// StorageClass, VolumeSnapshotClass, IngressClass, GatewayClass, ClusterClass, etc
	var classStage []*unstructured.Unstructured

	// contains all objects except for CRDs, Namespaces and Class type objects
	var resStage []*unstructured.Unstructured

	// contains the objects' metadata after apply
	resultSet := ssa.NewChangeSet()

	for _, u := range objects {
		if decryptor.IsEncryptedSecret(u) {
			return false, nil,
				fmt.Errorf("%s is SOPS encrypted, configuring decryption is required for this secret to be reconciled",
					ssautil.FmtUnstructured(u))
		}

		switch {
		case ssautil.IsClusterDefinition(u):
			defStage = append(defStage, u)
		case strings.HasSuffix(u.GetKind(), "Class"):
			classStage = append(classStage, u)
		default:
			resStage = append(resStage, u)
		}

	}

	var changeSetLog strings.Builder

	// validate, apply and wait for CRDs and Namespaces to register
	if len(defStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, defStage, applyOpts)
		if err != nil {
			return false, nil, err
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			if r.GroupChangeLog {
				log.Info("server-side apply for cluster definitions completed", "output", changeSet.ToGroupedMap())
			} else {
				log.Info("server-side apply for cluster definitions completed", "output", changeSet.ToMap())
			}
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}

			if err := manager.WaitForSet(changeSet.ToObjMetadataSet(), ssa.WaitOptions{
				Interval: 2 * time.Second,
				Timeout:  obj.GetTimeout(),
			}); err != nil {
				return false, nil, err
			}
		}
	}

	// validate, apply and wait for Class type objects to register
	if len(classStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, classStage, applyOpts)
		if err != nil {
			return false, nil, err
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			if r.GroupChangeLog {
				log.Info("server-side apply for cluster definitions completed", "output", changeSet.ToGroupedMap())
			} else {
				log.Info("server-side apply for cluster class types completed", "output", changeSet.ToMap())
			}
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}

			if err := manager.WaitForSet(changeSet.ToObjMetadataSet(), ssa.WaitOptions{
				Interval: 2 * time.Second,
				Timeout:  obj.GetTimeout(),
			}); err != nil {
				return false, nil, err
			}
		}
	}

	// sort by kind, validate and apply all the others objects
	sort.Sort(ssa.SortableUnstructureds(resStage))
	if len(resStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, resStage, applyOpts)
		if err != nil {
			return false, nil, fmt.Errorf("%w\n%s", err, changeSetLog.String())
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			if r.GroupChangeLog {
				log.Info("server-side apply for cluster definitions completed", "output", changeSet.ToGroupedMap())
			} else {
				log.Info("server-side apply completed", "output", changeSet.ToMap(), "revision", revision)
			}
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}
		}
	}

	// emit event only if the server-side apply resulted in changes
	applyLog := strings.TrimSuffix(changeSetLog.String(), "\n")
	if applyLog != "" {
		r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, applyLog, nil)
	}

	return applyLog != "", resultSet, nil
}

func (r *KustomizationReconciler) checkHealth(ctx context.Context,
	manager *ssa.ResourceManager,
	patcher *patch.SerialPatcher,
	obj *kustomizev1.Kustomization,
	revision string,
	originRevision string,
	isNewRevision bool,
	drifted bool,
	objects object.ObjMetadataSet) error {
	if len(obj.Spec.HealthChecks) == 0 && !obj.Spec.Wait {
		conditions.Delete(obj, meta.HealthyCondition)
		return nil
	}

	checkStart := time.Now()
	var err error
	if !obj.Spec.Wait {
		objects, err = inventory.ReferenceToObjMetadataSet(obj.Spec.HealthChecks)
		if err != nil {
			return err
		}
	}

	if len(objects) == 0 {
		conditions.Delete(obj, meta.HealthyCondition)
		return nil
	}

	// Guard against deadlock (waiting on itself).
	var toCheck []object.ObjMetadata
	for _, o := range objects {
		if o.GroupKind.Kind == kustomizev1.KustomizationKind &&
			o.Name == obj.GetName() &&
			o.Namespace == obj.GetNamespace() {
			continue
		}
		toCheck = append(toCheck, o)
	}

	// Find the previous health check result.
	wasHealthy := apimeta.IsStatusConditionTrue(obj.Status.Conditions, meta.HealthyCondition)

	// Update status with the reconciliation progress.
	message := fmt.Sprintf("Running health checks for revision %s with a timeout of %s", revision, obj.GetTimeout().String())
	conditions.MarkReconciling(obj, meta.ProgressingReason, "%s", message)
	conditions.MarkUnknown(obj, meta.HealthyCondition, meta.ProgressingReason, "%s", message)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("unable to update the healthy status to progressing: %w", err)
	}

	// Check the health with a default timeout of 30sec shorter than the reconciliation interval.
	if err := manager.WaitForSet(toCheck, ssa.WaitOptions{
		Interval: 5 * time.Second,
		Timeout:  obj.GetTimeout(),
		FailFast: r.FailFast,
	}); err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.HealthCheckFailedReason, "%s", err)
		conditions.MarkFalse(obj, meta.HealthyCondition, meta.HealthCheckFailedReason, "%s", err)
		return fmt.Errorf("health check failed after %s: %w", time.Since(checkStart).String(), err)
	}

	// Emit recovery event if the previous health check failed.
	msg := fmt.Sprintf("Health check passed in %s", time.Since(checkStart).String())
	if !wasHealthy || (isNewRevision && drifted) {
		r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, msg, nil)
	}

	conditions.MarkTrue(obj, meta.HealthyCondition, meta.SucceededReason, "%s", msg)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("unable to update the healthy status to progressing: %w", err)
	}

	return nil
}

func (r *KustomizationReconciler) prune(ctx context.Context,
	manager *ssa.ResourceManager,
	obj *kustomizev1.Kustomization,
	revision string,
	originRevision string,
	objects []*unstructured.Unstructured) (bool, error) {
	if !obj.Spec.Prune {
		return false, nil
	}

	log := ctrl.LoggerFrom(ctx)

	opts := ssa.DeleteOptions{
		PropagationPolicy: metav1.DeletePropagationBackground,
		Inclusions:        manager.GetOwnerLabels(obj.Name, obj.Namespace),
		Exclusions: map[string]string{
			fmt.Sprintf("%s/prune", kustomizev1.GroupVersion.Group):     kustomizev1.DisabledValue,
			fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
		},
	}

	changeSet, err := manager.DeleteAll(ctx, objects, opts)
	if err != nil {
		return false, err
	}

	// emit event only if the prune operation resulted in changes
	if changeSet != nil && len(changeSet.Entries) > 0 {
		log.Info(fmt.Sprintf("garbage collection completed: %s", changeSet.String()))
		r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, changeSet.String(), nil)
		return true, nil
	}

	return false, nil
}

func finalizerShouldDeleteResources(obj *kustomizev1.Kustomization) bool {
	if obj.GetDeletionPolicy() == kustomizev1.DeletionPolicyMirrorPrune {
		return obj.Spec.Prune
	}
	return obj.Spec.DeletionPolicy == kustomizev1.DeletionPolicyDelete
}

func (r *KustomizationReconciler) finalize(ctx context.Context,
	obj *kustomizev1.Kustomization) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if finalizerShouldDeleteResources(obj) &&
		!obj.Spec.Suspend &&
		obj.Status.Inventory != nil &&
		obj.Status.Inventory.Entries != nil {
		objects, _ := inventory.List(obj.Status.Inventory)

		var impersonatorOpts []runtimeClient.ImpersonatorOption
		if r.DefaultServiceAccount != "" || obj.Spec.ServiceAccountName != "" {
			impersonatorOpts = append(impersonatorOpts,
				runtimeClient.WithServiceAccount(r.DefaultServiceAccount, obj.Spec.ServiceAccountName, obj.GetNamespace()))
		}
		if obj.Spec.KubeConfig != nil {
			impersonatorOpts = append(impersonatorOpts,
				runtimeClient.WithKubeConfig(obj.Spec.KubeConfig, r.KubeConfigOpts, obj.GetNamespace()))
		}
		if r.ClusterReader != nil {
			impersonatorOpts = append(impersonatorOpts, runtimeClient.WithPolling(r.ClusterReader))
		}
		impersonation := runtimeClient.NewImpersonator(r.Client, impersonatorOpts...)
		if impersonation.CanImpersonate(ctx) {
			kubeClient, _, err := impersonation.GetClient(ctx)
			if err != nil {
				return ctrl.Result{}, err
			}

			resourceManager := ssa.NewResourceManager(kubeClient, nil, ssa.Owner{
				Field: r.ControllerName,
				Group: kustomizev1.GroupVersion.Group,
			})

			opts := ssa.DeleteOptions{
				PropagationPolicy: metav1.DeletePropagationBackground,
				Inclusions:        resourceManager.GetOwnerLabels(obj.Name, obj.Namespace),
				Exclusions: map[string]string{
					fmt.Sprintf("%s/prune", kustomizev1.GroupVersion.Group):     kustomizev1.DisabledValue,
					fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
				},
			}

			changeSet, err := resourceManager.DeleteAll(ctx, objects, opts)
			if err != nil {
				r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityError, "pruning for deleted resource failed", nil)
				// Return the error so we retry the failed garbage collection
				return ctrl.Result{}, err
			}

			if changeSet != nil && len(changeSet.Entries) > 0 {
				r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityInfo, changeSet.String(), nil)
			}
		} else {
			// when the account to impersonate is gone, log the stale objects and continue with the finalization
			msg := fmt.Sprintf("unable to prune objects: \n%s", ssautil.FmtUnstructuredList(objects))
			log.Error(fmt.Errorf("skiping pruning, failed to find account to impersonate"), msg)
			r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityError, msg, nil)
		}
	}

	// Remove our finalizer from the list and update it
	controllerutil.RemoveFinalizer(obj, kustomizev1.KustomizationFinalizer)
	// Stop reconciliation as the object is being deleted
	return ctrl.Result{}, nil
}

func (r *KustomizationReconciler) event(obj *kustomizev1.Kustomization,
	revision, originRevision, severity, msg string,
	metadata map[string]string) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if revision != "" {
		metadata[kustomizev1.GroupVersion.Group+"/"+eventv1.MetaRevisionKey] = revision
	}
	if originRevision != "" {
		metadata[kustomizev1.GroupVersion.Group+"/"+eventv1.MetaOriginRevisionKey] = originRevision
	}

	reason := severity
	if r := conditions.GetReason(obj, meta.ReadyCondition); r != "" {
		reason = r
	}

	eventtype := "Normal"
	if severity == eventv1.EventSeverityError {
		eventtype = "Warning"
	}

	r.EventRecorder.AnnotatedEventf(obj, metadata, eventtype, reason, msg)
}

func (r *KustomizationReconciler) finalizeStatus(ctx context.Context,
	obj *kustomizev1.Kustomization,
	patcher *patch.SerialPatcher) error {
	// Set the value of the reconciliation request in status.
	if v, ok := meta.ReconcileAnnotationValue(obj.GetAnnotations()); ok {
		obj.Status.LastHandledReconcileAt = v
	}

	// Remove the Reconciling condition and update the observed generation
	// if the reconciliation was successful.
	if conditions.IsTrue(obj, meta.ReadyCondition) {
		conditions.Delete(obj, meta.ReconcilingCondition)
		obj.Status.ObservedGeneration = obj.Generation
	}

	// Set the Reconciling reason to ProgressingWithRetry if the
	// reconciliation has failed.
	if conditions.IsFalse(obj, meta.ReadyCondition) &&
		conditions.Has(obj, meta.ReconcilingCondition) {
		rc := conditions.Get(obj, meta.ReconcilingCondition)
		rc.Reason = meta.ProgressingWithRetryReason
		conditions.Set(obj, rc)
	}

	// Patch finalizers, status and conditions.
	return r.patch(ctx, obj, patcher)
}

func (r *KustomizationReconciler) patch(ctx context.Context,
	obj *kustomizev1.Kustomization,
	patcher *patch.SerialPatcher) (retErr error) {

	// Configure the runtime patcher.
	patchOpts := []patch.Option{}
	ownedConditions := []string{
		meta.HealthyCondition,
		meta.ReadyCondition,
		meta.ReconcilingCondition,
		meta.StalledCondition,
	}
	patchOpts = append(patchOpts,
		patch.WithOwnedConditions{Conditions: ownedConditions},
		patch.WithForceOverwriteConditions{},
		patch.WithFieldOwner(r.statusManager),
	)

	// Patch the object status, conditions and finalizers.
	if err := patcher.Patch(ctx, obj, patchOpts...); err != nil {
		if !obj.GetDeletionTimestamp().IsZero() {
			err = kerrors.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
		}
		retErr = kerrors.NewAggregate([]error{retErr, err})
		if retErr != nil {
			return retErr
		}
	}

	return nil
}

// getOriginRevision returns the origin revision of the source artifact,
// or the empty string if it's not present, or if the artifact itself
// is not present.
func getOriginRevision(src sourcev1.Source) string {
	a := src.GetArtifact()
	if a == nil {
		return ""
	}
	return a.Metadata[OCIArtifactOriginRevisionAnnotation]
}
