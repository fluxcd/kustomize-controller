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
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	celtypes "github.com/google/cel-go/common/types"
	"github.com/opencontainers/go-digest"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	kuberecorder "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling/engine"
	"github.com/fluxcd/cli-utils/pkg/object"
	apiacl "github.com/fluxcd/pkg/apis/acl"
	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/auth"
	authutils "github.com/fluxcd/pkg/auth/utils"
	"github.com/fluxcd/pkg/cache"
	"github.com/fluxcd/pkg/http/fetch"
	generator "github.com/fluxcd/pkg/kustomize"
	"github.com/fluxcd/pkg/runtime/acl"
	"github.com/fluxcd/pkg/runtime/cel"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/conditions"
	runtimeCtrl "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/jitter"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/fluxcd/pkg/runtime/statusreaders"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/normalize"
	ssautil "github.com/fluxcd/pkg/ssa/utils"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/kustomize-controller/internal/decryptor"
	"github.com/fluxcd/kustomize-controller/internal/features"
	"github.com/fluxcd/kustomize-controller/internal/inventory"
)

// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets;ocirepositories;gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets/status;ocirepositories/status;gitrepositories/status,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	kuberecorder.EventRecorder
	runtimeCtrl.Metrics

	// Kubernetes options

	APIReader      client.Reader
	ClusterReader  engine.ClusterReaderFactory
	ConcurrentSSA  int
	ControllerName string
	KubeConfigOpts runtimeClient.KubeConfigOptions
	Mapper         apimeta.RESTMapper
	StatusManager  string

	// Multi-tenancy and security options

	DefaultServiceAccount   string
	DisallowedFieldManagers []string
	NoCrossNamespaceRefs    bool
	NoRemoteBases           bool
	SOPSAgeSecret           string
	TokenCache              *cache.TokenCache

	// Retry and requeue options

	ArtifactFetchRetries      int
	DependencyRequeueInterval time.Duration

	// Feature gates

	AdditiveCELDependencyCheck     bool
	AllowExternalArtifact          bool
	CancelHealthCheckOnNewRevision bool
	FailFast                       bool
	GroupChangeLog                 bool
	StrictSubstitutions            bool
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

		// Do not proceed if the Kustomization is suspended
		if obj.Spec.Suspend {
			return
		}

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
		errMsg := fmt.Sprintf("%s: %v", TerminalErrorMessage, err)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.InvalidCELExpressionReason, "%s", errMsg)
		conditions.MarkStalled(obj, meta.InvalidCELExpressionReason, "%s", errMsg)
		obj.Status.ObservedGeneration = obj.Generation
		r.event(obj, "", "", eventv1.EventSeverityError, errMsg, nil)
		return ctrl.Result{}, reconcile.TerminalError(err)
	}

	// Check object-level workload identity feature gate and decryption with service account.
	if d := obj.Spec.Decryption; d != nil && d.ServiceAccountName != "" && !auth.IsObjectLevelWorkloadIdentityEnabled() {
		const gate = auth.FeatureGateObjectLevelWorkloadIdentity
		const msgFmt = "to use spec.decryption.serviceAccountName for decryption authentication please enable the %s feature gate in the controller"
		msg := fmt.Sprintf(msgFmt, gate)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.FeatureGateDisabledReason, msgFmt, gate)
		conditions.MarkStalled(obj, meta.FeatureGateDisabledReason, msgFmt, gate)
		log.Error(auth.ErrObjectLevelWorkloadIdentityNotEnabled, msg)
		r.event(obj, "", "", eventv1.EventSeverityError, msg, nil)
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
			conditions.MarkStalled(obj, apiacl.AccessDeniedReason, "%s", err)
			r.event(obj, "", "", eventv1.EventSeverityError, err.Error(), nil)
			return ctrl.Result{}, reconcile.TerminalError(err)
		}

		// Retry with backoff on transient errors.
		return ctrl.Result{}, err
	}

	// Requeue the reconciliation if the source artifact is not found.
	if artifactSource.GetArtifact() == nil {
		msg := fmt.Sprintf("Source artifact not found, retrying in %s", r.DependencyRequeueInterval.String())
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", msg)
		log.Info(msg)
		return ctrl.Result{RequeueAfter: r.DependencyRequeueInterval}, nil
	}
	revision := artifactSource.GetArtifact().Revision
	originRevision := getOriginRevision(artifactSource)

	// Check dependencies and requeue the reconciliation if the check fails.
	if len(obj.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(ctx, obj, artifactSource); err != nil {
			// Check if this is a terminal error that should not trigger retries
			if errors.Is(err, reconcile.TerminalError(nil)) {
				errMsg := fmt.Sprintf("%s: %v", TerminalErrorMessage, err)
				conditions.MarkFalse(obj, meta.ReadyCondition, meta.InvalidCELExpressionReason, "%s", errMsg)
				conditions.MarkStalled(obj, meta.InvalidCELExpressionReason, "%s", errMsg)
				obj.Status.ObservedGeneration = obj.Generation
				r.event(obj, revision, originRevision, eventv1.EventSeverityError, errMsg, nil)
				return ctrl.Result{}, err
			}

			// Retry on transient errors.
			conditions.MarkFalse(obj, meta.ReadyCondition, meta.DependencyNotReadyReason, "%s", err)
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.DependencyRequeueInterval.String())
			log.Info(msg)
			r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, msg, nil)
			return ctrl.Result{RequeueAfter: r.DependencyRequeueInterval}, nil
		}
		log.Info("All dependencies are ready, proceeding with reconciliation")
	}

	// Reconcile the latest revision.
	reconcileErr := r.reconcile(ctx, obj, artifactSource, patcher, statusReaders)

	// Requeue at the specified retry interval if the artifact tarball is not found.
	if errors.Is(reconcileErr, fetch.ErrFileNotFound) {
		msg := fmt.Sprintf("Source is not ready, artifact not found, retrying in %s", r.DependencyRequeueInterval.String())
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", msg)
		log.Info(msg)
		return ctrl.Result{RequeueAfter: r.DependencyRequeueInterval}, nil
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
	reconcileStart := time.Now()
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

	// Set the artifact URL hostname override for localhost access.
	sourceLocalhost := os.Getenv("SOURCE_CONTROLLER_LOCALHOST")
	if strings.Contains(src.GetArtifact().URL, "//source-watcher") {
		sourceLocalhost = os.Getenv("SOURCE_WATCHER_LOCALHOST")
	}

	// Download artifact and extract files to the tmp dir.
	fetcher := fetch.New(
		fetch.WithLogger(ctrl.LoggerFrom(ctx)),
		fetch.WithRetries(r.ArtifactFetchRetries),
		fetch.WithMaxDownloadSize(tar.UnlimitedUntarSize),
		fetch.WithUntar(tar.WithMaxUntarSize(tar.UnlimitedUntarSize)),
		fetch.WithHostnameOverwrite(sourceLocalhost),
	)
	if err = fetcher.Fetch(src.GetArtifact().URL, src.GetArtifact().Digest, tmpDir); err != nil {
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
	var mustImpersonate bool
	if r.DefaultServiceAccount != "" || obj.Spec.ServiceAccountName != "" {
		mustImpersonate = true
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithServiceAccount(r.DefaultServiceAccount, obj.Spec.ServiceAccountName, obj.GetNamespace()))
	}
	if obj.Spec.KubeConfig != nil {
		mustImpersonate = true
		provider := r.getProviderRESTConfigFetcher(obj)
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithKubeConfig(obj.Spec.KubeConfig, r.KubeConfigOpts, obj.GetNamespace(), provider))
	}
	if r.ClusterReader != nil || len(statusReaders) > 0 {
		impersonatorOpts = append(impersonatorOpts,
			runtimeClient.WithPolling(r.ClusterReader, statusReaders...))
	}
	impersonation := runtimeClient.NewImpersonator(r.Client, impersonatorOpts...)

	// Create the Kubernetes client that runs under impersonation.
	var kubeClient client.Client
	var statusPoller *polling.StatusPoller
	if mustImpersonate {
		kubeClient, statusPoller, err = impersonation.GetClient(ctx)
	} else {
		kubeClient, statusPoller = r.getClientAndPoller(statusReaders)
	}
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

	// Calculate the digest of the built resources for history tracking.
	checksum := digest.FromBytes(resources).String()
	historyMeta := map[string]string{"revision": revision}
	if originRevision != "" {
		historyMeta["originRevision"] = originRevision
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
	drifted, changeSetWithSkipped, err := r.apply(ctx, resourceManager, obj, revision, originRevision, objects)
	if err != nil {
		obj.Status.History.Upsert(checksum, time.Now(), time.Since(reconcileStart), meta.ReconciliationFailedReason, historyMeta)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Filter out skipped entries from the change set.
	changeSet := ssa.NewChangeSet()
	skippedSet := make(map[object.ObjMetadata]struct{})
	for _, entry := range changeSetWithSkipped.Entries {
		if entry.Action == ssa.SkippedAction {
			skippedSet[entry.ObjMetadata] = struct{}{}
		} else {
			changeSet.Add(entry)
		}
	}

	// Create an inventory from the reconciled resources.
	newInventory := inventory.New()
	err = inventory.AddChangeSet(newInventory, changeSet)
	if err != nil {
		obj.Status.History.Upsert(checksum, time.Now(), time.Since(reconcileStart), meta.ReconciliationFailedReason, historyMeta)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Set last applied inventory in status.
	obj.Status.Inventory = newInventory

	// Detect stale resources which are subject to garbage collection.
	staleObjects, err := inventory.Diff(oldInventory, newInventory, skippedSet)
	if err != nil {
		obj.Status.History.Upsert(checksum, time.Now(), time.Since(reconcileStart), meta.ReconciliationFailedReason, historyMeta)
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return err
	}

	// Run garbage collection for stale resources that do not have pruning disabled.
	if _, err := r.prune(ctx, resourceManager, obj, revision, originRevision, staleObjects); err != nil {
		obj.Status.History.Upsert(checksum, time.Now(), time.Since(reconcileStart), meta.PruneFailedReason, historyMeta)
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
		obj.Status.History.Upsert(checksum, time.Now(), time.Since(reconcileStart), meta.HealthCheckFailedReason, historyMeta)
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
	obj.Status.History.Upsert(checksum,
		time.Now(),
		time.Since(reconcileStart),
		meta.ReconciliationSucceededReason,
		historyMeta)

	return nil
}

// checkDependencies checks if the dependencies of the current Kustomization are ready.
// To be considered ready, a dependencies must meet the following criteria:
// - The dependency exists in the API server.
// - The CEL expression (if provided) must evaluate to true.
// - The dependency observed generation must match the current generation.
// - The dependency Ready condition must be true.
// - The dependency last applied revision must match the current source artifact revision.
func (r *KustomizationReconciler) checkDependencies(ctx context.Context,
	obj *kustomizev1.Kustomization,
	source sourcev1.Source) error {

	// Convert the Kustomization object to Unstructured for CEL evaluation.
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert Kustomization to unstructured: %w", err)
	}

	for _, depRef := range obj.Spec.DependsOn {
		// Check if the dependency exists by querying
		// the API server bypassing the cache.
		if depRef.Namespace == "" {
			depRef.Namespace = obj.GetNamespace()
		}
		depName := types.NamespacedName{
			Namespace: depRef.Namespace,
			Name:      depRef.Name,
		}
		var dep kustomizev1.Kustomization
		err := r.APIReader.Get(ctx, depName, &dep)
		if err != nil {
			return fmt.Errorf("dependency '%s' not found: %w", depName, err)
		}

		// Evaluate the CEL expression (if specified) to determine if the dependency is ready.
		if depRef.ReadyExpr != "" {
			ready, err := r.evalReadyExpr(ctx, depRef.ReadyExpr, objMap, &dep)
			if err != nil {
				return err
			}
			if !ready {
				return fmt.Errorf("dependency '%s' is not ready according to readyExpr eval", depName)
			}
		}

		// Skip the built-in readiness check if the CEL expression is provided
		// and the AdditiveCELDependencyCheck feature gate is not enabled.
		if depRef.ReadyExpr != "" && !r.AdditiveCELDependencyCheck {
			continue
		}

		// Check if the dependency observed generation is up to date
		// and if the dependency is in a ready state.
		if len(dep.Status.Conditions) == 0 || dep.Generation != dep.Status.ObservedGeneration {
			return fmt.Errorf("dependency '%s' is not ready", depName)
		}
		if !apimeta.IsStatusConditionTrue(dep.Status.Conditions, meta.ReadyCondition) {
			return fmt.Errorf("dependency '%s' is not ready", depName)
		}

		// Check if the dependency source matches the current source
		// and if so, verify that the last applied revision of the dependency
		// matches the current source artifact revision.
		srcNamespace := dep.Spec.SourceRef.Namespace
		if srcNamespace == "" {
			srcNamespace = dep.GetNamespace()
		}
		depSrcNamespace := obj.Spec.SourceRef.Namespace
		if depSrcNamespace == "" {
			depSrcNamespace = obj.GetNamespace()
		}
		if dep.Spec.SourceRef.Name == obj.Spec.SourceRef.Name &&
			srcNamespace == depSrcNamespace &&
			dep.Spec.SourceRef.Kind == obj.Spec.SourceRef.Kind &&
			!source.GetArtifact().HasRevision(dep.Status.LastAppliedRevision) {
			return fmt.Errorf("dependency '%s' revision is not up to date", depName)
		}
	}

	return nil
}

// evalReadyExpr evaluates the CEL expression for the dependency readiness check.
func (r *KustomizationReconciler) evalReadyExpr(
	ctx context.Context,
	expr string,
	selfMap map[string]any,
	dep *kustomizev1.Kustomization,
) (bool, error) {
	const (
		selfName = "self"
		depName  = "dep"
	)

	celExpr, err := cel.NewExpression(expr,
		cel.WithCompile(),
		cel.WithOutputType(celtypes.BoolType),
		cel.WithStructVariables(selfName, depName))
	if err != nil {
		return false, reconcile.TerminalError(fmt.Errorf("failed to evaluate dependency %s: %w", dep.Name, err))
	}

	depMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dep)
	if err != nil {
		return false, fmt.Errorf("failed to convert %s object to map: %w", depName, err)
	}

	vars := map[string]any{
		selfName: selfMap,
		depName:  depMap,
	}

	return celExpr.EvaluateBoolean(ctx, vars)
}

// getSource resolves the source reference and returns the source object containing the artifact.
// It returns an error if the source is not found or if access is denied.
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

	// Check if cross-namespace references are allowed.
	if r.NoCrossNamespaceRefs && sourceNamespace != obj.GetNamespace() {
		return src, acl.AccessDeniedError(
			fmt.Sprintf("can't access '%s/%s', cross-namespace references have been blocked",
				obj.Spec.SourceRef.Kind, namespacedName))
	}

	// Check if ExternalArtifact kind is allowed.
	if obj.Spec.SourceRef.Kind == sourcev1.ExternalArtifactKind && !r.AllowExternalArtifact {
		return src, acl.AccessDeniedError(
			fmt.Sprintf("can't access '%s/%s', %s feature gate is disabled",
				obj.Spec.SourceRef.Kind, namespacedName, features.ExternalArtifact))
	}

	switch obj.Spec.SourceRef.Kind {
	case sourcev1.OCIRepositoryKind:
		var repository sourcev1.OCIRepository
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
	case sourcev1.ExternalArtifactKind:
		var ea sourcev1.ExternalArtifact
		err := r.Client.Get(ctx, namespacedName, &ea)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &ea
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

	// Build decryptor.
	decryptorOpts := []decryptor.Option{
		decryptor.WithRoot(workDir),
	}
	if r.TokenCache != nil {
		decryptorOpts = append(decryptorOpts, decryptor.WithTokenCache(*r.TokenCache))
	}
	if name, ns := r.SOPSAgeSecret, os.Getenv(runtimeCtrl.EnvRuntimeNamespace); name != "" && ns != "" {
		decryptorOpts = append(decryptorOpts, decryptor.WithSOPSAgeSecret(name, ns))
	}
	dec, cleanup, err := decryptor.New(r.Client, obj, decryptorOpts...)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Import keys and static credentials for decryption.
	if err := dec.ImportKeys(ctx); err != nil {
		return nil, err
	}

	// Set options for secret-less authentication with cloud providers for decryption.
	dec.SetAuthOptions(ctx)

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

	for _, u := range objects {
		if decryptor.IsEncryptedSecret(u) && !decryptor.IsDecryptionDisabled(u.GetAnnotations()) {
			return false, nil,
				fmt.Errorf("%s is SOPS encrypted, configuring decryption is required for this secret to be reconciled",
					ssautil.FmtUnstructured(u))
		}
	}

	// contains the objects' metadata after apply
	resultSet := ssa.NewChangeSet()
	var changeSetLog strings.Builder

	if len(objects) > 0 {
		changeSet, err := manager.ApplyAllStaged(ctx, objects, applyOpts)

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			// filter out the objects that have not changed
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}
		}

		// include the change log in the error message in case af a partial apply
		if err != nil {
			return false, nil, fmt.Errorf("%w\n%s", err, changeSetLog.String())
		}

		// log all applied objects
		if changeSet != nil && len(changeSet.Entries) > 0 {
			if r.GroupChangeLog {
				log.Info("server-side apply completed", "output", changeSet.ToGroupedMap(), "revision", revision)
			} else {
				log.Info("server-side apply completed", "output", changeSet.ToMap(), "revision", revision)
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
	healthCtx := ctx
	if r.CancelHealthCheckOnNewRevision {
		// Create a cancellable context for health checks that monitors for new revisions
		var cancel context.CancelFunc
		healthCtx, cancel = context.WithCancel(ctx)
		defer cancel()

		// Start monitoring for new revisions to allow early cancellation
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-healthCtx.Done():
					return
				case <-ticker.C:
					// Get the latest source artifact
					latestSrc, err := r.getSource(ctx, obj)
					if err == nil && latestSrc.GetArtifact() != nil {
						if newRevision := latestSrc.GetArtifact().Revision; newRevision != revision {
							const msg = "New revision detected during health check, cancelling"
							r.event(obj, revision, originRevision, eventv1.EventSeverityInfo, msg, nil)
							ctrl.LoggerFrom(ctx).Info(msg, "current", revision, "new", newRevision)
							cancel()
							return
						}
					}
				}
			}
		}()
	}
	if err := manager.WaitForSetWithContext(healthCtx, toCheck, ssa.WaitOptions{
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

// finalizerShouldDeleteResources determines if resources should be deleted
// based on the object's inventory and deletion policy.
// A suspended Kustomization or one without an inventory will not delete resources.
func finalizerShouldDeleteResources(obj *kustomizev1.Kustomization) bool {
	if obj.Spec.Suspend {
		return false
	}

	if obj.Status.Inventory == nil || len(obj.Status.Inventory.Entries) == 0 {
		return false
	}

	switch obj.GetDeletionPolicy() {
	case kustomizev1.DeletionPolicyMirrorPrune:
		return obj.Spec.Prune
	case kustomizev1.DeletionPolicyDelete:
		return true
	case kustomizev1.DeletionPolicyWaitForTermination:
		return true
	default:
		return false
	}
}

// finalize handles the finalization logic for a Kustomization resource during its deletion process.
// Managed resources are pruned based on the deletion policy and suspended state of the Kustomization.
// When the policy is set to WaitForTermination, the function blocks and waits for the resources
// to be terminated by the Kubernetes Garbage Collector for the specified timeout duration.
// If the service account used for impersonation is no longer available or if a timeout occurs
// while waiting for resources to be terminated, an error is logged and the finalizer is removed.
func (r *KustomizationReconciler) finalize(ctx context.Context,
	obj *kustomizev1.Kustomization) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if finalizerShouldDeleteResources(obj) {
		objects, _ := inventory.List(obj.Status.Inventory)

		var impersonatorOpts []runtimeClient.ImpersonatorOption
		var mustImpersonate bool
		if r.DefaultServiceAccount != "" || obj.Spec.ServiceAccountName != "" {
			mustImpersonate = true
			impersonatorOpts = append(impersonatorOpts,
				runtimeClient.WithServiceAccount(r.DefaultServiceAccount, obj.Spec.ServiceAccountName, obj.GetNamespace()))
		}
		if obj.Spec.KubeConfig != nil {
			mustImpersonate = true
			provider := r.getProviderRESTConfigFetcher(obj)
			impersonatorOpts = append(impersonatorOpts,
				runtimeClient.WithKubeConfig(obj.Spec.KubeConfig, r.KubeConfigOpts, obj.GetNamespace(), provider))
		}
		if r.ClusterReader != nil {
			impersonatorOpts = append(impersonatorOpts, runtimeClient.WithPolling(r.ClusterReader))
		}
		impersonation := runtimeClient.NewImpersonator(r.Client, impersonatorOpts...)
		if impersonation.CanImpersonate(ctx) {
			var kubeClient client.Client
			var err error
			if mustImpersonate {
				kubeClient, _, err = impersonation.GetClient(ctx)
			} else {
				kubeClient = r.Client
			}
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
				// Emit event with the resources marked for deletion.
				r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityInfo, changeSet.String(), nil)

				// Wait for the resources marked for deletion to be terminated.
				if obj.GetDeletionPolicy() == kustomizev1.DeletionPolicyWaitForTermination {
					if err := resourceManager.WaitForSetTermination(changeSet, ssa.WaitOptions{
						Interval: 2 * time.Second,
						Timeout:  obj.GetTimeout(),
					}); err != nil {
						// Emit an event and log the error if a timeout occurs.
						msg := "failed to wait for resources termination"
						log.Error(err, msg)
						r.event(obj, obj.Status.LastAppliedRevision, obj.Status.LastAppliedOriginRevision, eventv1.EventSeverityError, msg, nil)
					}
				}
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

	// Cleanup caches.
	for _, op := range kustomizev1.AllMetrics {
		r.TokenCache.DeleteEventsForObject(kustomizev1.KustomizationKind, obj.GetName(), obj.GetNamespace(), op)
	}

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
		patch.WithFieldOwner(r.StatusManager),
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

// getClientAndPoller creates a status poller with the custom status readers
// from CEL expressions and the custom job status reader, and returns the
// Kubernetes client of the controller and the status poller.
// Should be used for reconciliations that are not configured to use
// ServiceAccount impersonation or kubeconfig.
func (r *KustomizationReconciler) getClientAndPoller(
	readerCtors []func(apimeta.RESTMapper) engine.StatusReader,
) (client.Client, *polling.StatusPoller) {

	readers := make([]engine.StatusReader, 0, 1+len(readerCtors))
	readers = append(readers, statusreaders.NewCustomJobStatusReader(r.Mapper))
	for _, ctor := range readerCtors {
		readers = append(readers, ctor(r.Mapper))
	}

	poller := polling.NewStatusPoller(r.Client, r.Mapper, polling.Options{
		CustomStatusReaders:  readers,
		ClusterReaderFactory: r.ClusterReader,
	})

	return r.Client, poller
}

// getProviderRESTConfigFetcher returns a ProviderRESTConfigFetcher for the
// Kustomization object, which is used to fetch the kubeconfig for a ConfigMap
// reference in the Kustomization spec.
func (r *KustomizationReconciler) getProviderRESTConfigFetcher(obj *kustomizev1.Kustomization) runtimeClient.ProviderRESTConfigFetcher {
	var provider runtimeClient.ProviderRESTConfigFetcher
	if kc := obj.Spec.KubeConfig; kc != nil && kc.SecretRef == nil && kc.ConfigMapRef != nil {
		var opts []auth.Option
		if r.TokenCache != nil {
			involvedObject := cache.InvolvedObject{
				Kind:      kustomizev1.KustomizationKind,
				Name:      obj.GetName(),
				Namespace: obj.GetNamespace(),
				Operation: kustomizev1.MetricFetchKubeConfig,
			}
			opts = append(opts, auth.WithCache(*r.TokenCache, involvedObject))
		}
		provider = runtimeClient.ProviderRESTConfigFetcher(authutils.GetRESTConfigFetcher(opts...))
	}
	return provider
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
