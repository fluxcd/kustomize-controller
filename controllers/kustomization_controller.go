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

package controllers

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/hashicorp/go-retryablehttp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/cli-utils/pkg/object"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/metrics"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
)

// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets;gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets/status;gitrepositories/status,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	httpClient            *retryablehttp.Client
	requeueDependency     time.Duration
	Scheme                *runtime.Scheme
	EventRecorder         kuberecorder.EventRecorder
	ExternalEventRecorder *events.Recorder
	MetricsRecorder       *metrics.Recorder
	StatusPoller          *polling.StatusPoller
	ControllerName        string
}

type KustomizationReconcilerOptions struct {
	MaxConcurrentReconciles   int
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	const (
		gitRepositoryIndexKey string = ".metadata.gitRepository"
		bucketIndexKey        string = ".metadata.bucket"
	)

	// Index the Kustomizations by the GitRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(context.TODO(), &kustomizev1.Kustomization{}, gitRepositoryIndexKey,
		r.indexBy(sourcev1.GitRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Kustomizations by the Bucket references they (may) point at.
	if err := mgr.GetCache().IndexField(context.TODO(), &kustomizev1.Kustomization{}, bucketIndexKey,
		r.indexBy(sourcev1.BucketKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	r.requeueDependency = opts.DependencyRequeueInterval

	// Configure the retryable http client used for fetching artifacts.
	// By default it retries 10 times within a 3.5 minutes window.
	httpClient := retryablehttp.NewClient()
	httpClient.RetryWaitMin = 5 * time.Second
	httpClient.RetryWaitMax = 30 * time.Second
	httpClient.RetryMax = opts.HTTPRetry
	httpClient.Logger = nil
	r.httpClient = httpClient

	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&source.Kind{Type: &sourcev1.GitRepository{}},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(gitRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&source.Kind{Type: &sourcev1.Bucket{}},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(bucketIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *KustomizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	reconcileStart := time.Now()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Record suspended status metric
	defer r.recordSuspension(ctx, kustomization)

	// Add our finalizer if it does not exist
	if !controllerutil.ContainsFinalizer(&kustomization, kustomizev1.KustomizationFinalizer) {
		controllerutil.AddFinalizer(&kustomization, kustomizev1.KustomizationFinalizer)
		if err := r.Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to register finalizer")
			return ctrl.Result{}, err
		}
	}

	// Examine if the object is under deletion
	if !kustomization.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, kustomization)
	}

	// Return early if the Kustomization is suspended.
	if kustomization.Spec.Suspend {
		log.Info("Reconciliation is suspended for this object")
		return ctrl.Result{}, nil
	}

	// resolve source reference
	source, err := r.getSource(ctx, kustomization)
	if err != nil {
		if apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Source '%s' not found", kustomization.Spec.SourceRef.String())
			kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.ArtifactFailedReason, msg)
			if err := r.patchStatus(ctx, req, kustomization.Status); err != nil {
				log.Error(err, "unable to update status for source not found")
				return ctrl.Result{Requeue: true}, err
			}
			r.recordReadiness(ctx, kustomization)
			log.Info(msg)
			// do not requeue immediately, when the source is created the watcher should trigger a reconciliation
			return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, nil
		} else {
			// retry on transient errors
			return ctrl.Result{Requeue: true}, err
		}
	}

	if source.GetArtifact() == nil {
		msg := "Source is not ready, artifact not found"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.ArtifactFailedReason, msg)
		if err := r.patchStatus(ctx, req, kustomization.Status); err != nil {
			log.Error(err, "unable to update status for artifact not found")
			return ctrl.Result{Requeue: true}, err
		}
		r.recordReadiness(ctx, kustomization)
		log.Info(msg)
		// do not requeue immediately, when the artifact is created the watcher should trigger a reconciliation
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, nil
	}

	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(kustomization); err != nil {
			kustomization = kustomizev1.KustomizationNotReady(
				kustomization, source.GetArtifact().Revision, meta.DependencyNotReadyReason, err.Error())
			if err := r.patchStatus(ctx, req, kustomization.Status); err != nil {
				log.Error(err, "unable to update status for dependency not ready")
				return ctrl.Result{Requeue: true}, err
			}
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue on a fix interval.
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			log.Info(msg)
			r.event(ctx, kustomization, source.GetArtifact().Revision, events.EventSeverityInfo, msg, nil)
			r.recordReadiness(ctx, kustomization)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}
		log.Info("All dependencies are ready, proceeding with reconciliation")
	}

	// record reconciliation duration
	if r.MetricsRecorder != nil {
		objRef, err := reference.GetReference(r.Scheme, &kustomization)
		if err != nil {
			return ctrl.Result{}, err
		}
		defer r.MetricsRecorder.RecordDuration(*objRef, reconcileStart)
	}

	// set the reconciliation status to progressing
	kustomization = kustomizev1.KustomizationProgressing(kustomization, "reconciliation in progress")
	if err := r.patchStatus(ctx, req, kustomization.Status); err != nil {
		log.Error(err, "unable to update status to progressing")
		return ctrl.Result{Requeue: true}, err
	}
	r.recordReadiness(ctx, kustomization)

	// reconcile kustomization by applying the latest revision
	reconciledKustomization, reconcileErr := r.reconcile(ctx, *kustomization.DeepCopy(), source)
	if err := r.patchStatus(ctx, req, reconciledKustomization.Status); err != nil {
		log.Error(err, "unable to update status after reconciliation")
		return ctrl.Result{Requeue: true}, err
	}
	r.recordReadiness(ctx, reconciledKustomization)

	// broadcast the reconciliation failure and requeue at the specified retry interval
	if reconcileErr != nil {
		log.Error(reconcileErr, fmt.Sprintf("Reconciliation failed after %s, next try in %s",
			time.Since(reconcileStart).String(),
			kustomization.GetRetryInterval().String()),
			"revision",
			source.GetArtifact().Revision)
		r.event(ctx, reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityError,
			reconcileErr.Error(), nil)
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, nil
	}

	// broadcast the reconciliation result and requeue at the specified interval
	msg := fmt.Sprintf("Reconciliation finished in %s, next run in %s",
		time.Since(reconcileStart).String(),
		kustomization.Spec.Interval.Duration.String())
	log.Info(msg, "revision", source.GetArtifact().Revision)
	r.event(ctx, reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityInfo,
		msg, map[string]string{"commit_status": "update"})
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

func (r *KustomizationReconciler) reconcile(
	ctx context.Context,
	kustomization kustomizev1.Kustomization,
	source sourcev1.Source) (kustomizev1.Kustomization, error) {
	// record the value of the reconciliation request, if any
	if v, ok := meta.ReconcileAnnotationValue(kustomization.GetAnnotations()); ok {
		kustomization.Status.SetLastHandledReconcileRequest(v)
	}

	revision := source.GetArtifact().Revision

	// create tmp dir
	tmpDir, err := os.MkdirTemp("", kustomization.Name)
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			sourcev1.StorageOperationFailedReason,
			err.Error(),
		), err
	}
	defer os.RemoveAll(tmpDir)

	// download artifact and extract files
	err = r.download(source.GetArtifact(), tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, kustomization.Spec.Path)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// setup the Kubernetes client for impersonation
	impersonation := NewKustomizeImpersonation(kustomization, r.Client, r.StatusPoller, dirPath)
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), fmt.Errorf("failed to build kube client: %w", err)
	}

	// generate kustomization.yaml if needed
	err = r.generate(kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// build the kustomization
	resources, err := r.build(ctx, kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// convert the build result into Kubernetes unstructured objects
	objects, err := ssa.ReadObjects(bytes.NewReader(resources))
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// create a snapshot of the current inventory
	oldStatus := kustomization.Status.DeepCopy()

	// create the server-side apply manager
	resourceManager := ssa.NewResourceManager(kubeClient, statusPoller, ssa.Owner{
		Field: r.ControllerName,
		Group: kustomizev1.GroupVersion.Group,
	})
	resourceManager.SetOwnerLabels(objects, kustomization.GetName(), kustomization.GetNamespace())

	// validate and apply resources in stages
	drifted, changeSet, err := r.apply(ctx, resourceManager, kustomization, revision, objects)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), err
	}

	// create an inventory of objects to be reconciled
	newInventory := NewInventory()
	err = AddObjectsToInventory(newInventory, changeSet)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), err
	}

	// detect stale objects which are subject to garbage collection
	var staleObjects []*unstructured.Unstructured
	if oldStatus.Inventory != nil {
		diffObjects, err := DiffInventory(oldStatus.Inventory, newInventory)
		if err != nil {
			return kustomizev1.KustomizationNotReady(
				kustomization,
				revision,
				meta.ReconciliationFailedReason,
				err.Error(),
			), err
		}

		// TODO: remove this workaround after kustomize-controller 0.18 release
		// skip objects that were wrongly marked as namespaced
		// https://github.com/fluxcd/kustomize-controller/issues/466
		newObjects, _ := ListObjectsInInventory(newInventory)
		for _, obj := range diffObjects {
			preserve := false
			if obj.GetNamespace() != "" {
				for _, newObj := range newObjects {
					if newObj.GetNamespace() == "" &&
						obj.GetKind() == newObj.GetKind() &&
						obj.GetAPIVersion() == newObj.GetAPIVersion() &&
						obj.GetName() == newObj.GetName() {
						preserve = true
						break
					}
				}
			}
			if !preserve {
				staleObjects = append(staleObjects, obj)
			}
		}
	}

	// run garbage collection for stale objects that do not have pruning disabled
	if _, err := r.prune(ctx, resourceManager, kustomization, revision, staleObjects); err != nil {
		return kustomizev1.KustomizationNotReadyInventory(
			kustomization,
			newInventory,
			revision,
			kustomizev1.PruneFailedReason,
			err.Error(),
		), err
	}

	// health assessment
	if err := r.checkHealth(ctx, resourceManager, kustomization, revision, drifted, changeSet.ToObjMetadataSet()); err != nil {
		return kustomizev1.KustomizationNotReadyInventory(
			kustomization,
			newInventory,
			revision,
			kustomizev1.HealthCheckFailedReason,
			err.Error(),
		), err
	}

	return kustomizev1.KustomizationReadyInventory(
		kustomization,
		newInventory,
		revision,
		meta.ReconciliationSucceededReason,
		fmt.Sprintf("Applied revision: %s", revision),
	), nil
}

func (r *KustomizationReconciler) checkDependencies(kustomization kustomizev1.Kustomization) error {
	for _, d := range kustomization.Spec.DependsOn {
		if d.Namespace == "" {
			d.Namespace = kustomization.GetNamespace()
		}
		dName := types.NamespacedName(d)
		var k kustomizev1.Kustomization
		err := r.Get(context.Background(), dName, &k)
		if err != nil {
			return fmt.Errorf("unable to get '%s' dependency: %w", dName, err)
		}

		if len(k.Status.Conditions) == 0 || k.Generation != k.Status.ObservedGeneration {
			return fmt.Errorf("dependency '%s' is not ready", dName)
		}

		if !apimeta.IsStatusConditionTrue(k.Status.Conditions, meta.ReadyCondition) {
			return fmt.Errorf("dependency '%s' is not ready", dName)
		}
	}

	return nil
}

func (r *KustomizationReconciler) download(artifact *sourcev1.Artifact, tmpDir string) error {
	artifactURL := artifact.URL
	if hostname := os.Getenv("SOURCE_CONTROLLER_LOCALHOST"); hostname != "" {
		u, err := url.Parse(artifactURL)
		if err != nil {
			return err
		}
		u.Host = hostname
		artifactURL = u.String()
	}

	req, err := retryablehttp.NewRequest(http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create a new request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download artifact, error: %w", err)
	}
	defer resp.Body.Close()

	// check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download artifact from %s, status: %s", artifactURL, resp.Status)
	}

	var buf bytes.Buffer

	// verify checksum matches origin
	if err := r.verifyArtifact(artifact, &buf, resp.Body); err != nil {
		return err
	}

	// extract
	if _, err = untar.Untar(&buf, tmpDir); err != nil {
		return fmt.Errorf("failed to untar artifact, error: %w", err)
	}

	return nil
}

func (r *KustomizationReconciler) verifyArtifact(artifact *sourcev1.Artifact, buf *bytes.Buffer, reader io.Reader) error {
	hasher := sha256.New()

	// for backwards compatibility with source-controller v0.17.2 and older
	if len(artifact.Checksum) == 40 {
		hasher = sha1.New()
	}

	// compute checksum
	mw := io.MultiWriter(hasher, buf)
	if _, err := io.Copy(mw, reader); err != nil {
		return err
	}

	if checksum := fmt.Sprintf("%x", hasher.Sum(nil)); checksum != artifact.Checksum {
		return fmt.Errorf("failed to verify artifact: computed checksum '%s' doesn't match advertised '%s'",
			checksum, artifact.Checksum)
	}

	return nil
}

func (r *KustomizationReconciler) getSource(ctx context.Context, kustomization kustomizev1.Kustomization) (sourcev1.Source, error) {
	var source sourcev1.Source
	sourceNamespace := kustomization.GetNamespace()
	if kustomization.Spec.SourceRef.Namespace != "" {
		sourceNamespace = kustomization.Spec.SourceRef.Namespace
	}
	namespacedName := types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      kustomization.Spec.SourceRef.Name,
	}
	switch kustomization.Spec.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return source, err
			}
			return source, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		source = &repository
	case sourcev1.BucketKind:
		var bucket sourcev1.Bucket
		err := r.Client.Get(ctx, namespacedName, &bucket)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return source, err
			}
			return source, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		source = &bucket
	default:
		return source, fmt.Errorf("source `%s` kind '%s' not supported",
			kustomization.Spec.SourceRef.Name, kustomization.Spec.SourceRef.Kind)
	}
	return source, nil
}

func (r *KustomizationReconciler) generate(kustomization kustomizev1.Kustomization, dirPath string) error {
	gen := NewGenerator(kustomization)
	return gen.WriteFile(dirPath)
}

func (r *KustomizationReconciler) build(ctx context.Context, kustomization kustomizev1.Kustomization, dirPath string) ([]byte, error) {
	dec, cleanup, err := NewTempDecryptor(r.Client, kustomization)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// import OpenPGP keys if any
	if err := dec.ImportKeys(ctx); err != nil {
		return nil, err
	}

	fs := filesys.MakeFsOnDisk()
	// decrypt .env files before building kustomization
	if kustomization.Spec.Decryption != nil {
		if err = dec.decryptDotEnvFiles(dirPath); err != nil {
			return nil, fmt.Errorf("error decrypting .env file: %w", err)
		}
	}
	m, err := buildKustomization(fs, dirPath)
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed: %w", err)
	}

	for _, res := range m.Resources() {
		// check if resources are encrypted and decrypt them before generating the final YAML
		if kustomization.Spec.Decryption != nil {
			outRes, err := dec.Decrypt(res)
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
		if kustomization.Spec.PostBuild != nil {
			outRes, err := substituteVariables(ctx, r.Client, kustomization, res)
			if err != nil {
				return nil, fmt.Errorf("var substitution failed for '%s': %w", res.GetName(), err)
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

func (r *KustomizationReconciler) apply(ctx context.Context, manager *ssa.ResourceManager, kustomization kustomizev1.Kustomization, revision string, objects []*unstructured.Unstructured) (bool, *ssa.ChangeSet, error) {
	log := ctrl.LoggerFrom(ctx)

	if err := ssa.SetNativeKindsDefaults(objects); err != nil {
		return false, nil, err
	}

	applyOpts := ssa.DefaultApplyOptions()
	applyOpts.Force = kustomization.Spec.Force
	applyOpts.Exclusions = map[string]string{
		fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
	}

	// contains only CRDs and Namespaces
	var stageOne []*unstructured.Unstructured

	// contains all objects except for CRDs and Namespaces
	var stageTwo []*unstructured.Unstructured

	// contains the objects' metadata after apply
	resultSet := ssa.NewChangeSet()

	for _, u := range objects {
		if IsEncryptedSecret(u) {
			return false, nil,
				fmt.Errorf("%s is SOPS encryted, configuring decryption is required for this secret to be reconciled",
					ssa.FmtUnstructured(u))
		}

		if ssa.IsClusterDefinition(u) {
			stageOne = append(stageOne, u)
		} else {
			stageTwo = append(stageTwo, u)
		}
	}

	var changeSetLog strings.Builder

	// validate, apply and wait for CRDs and Namespaces to register
	if len(stageOne) > 0 {
		changeSet, err := manager.ApplyAll(ctx, stageOne, applyOpts)
		if err != nil {
			return false, nil, err
		}
		resultSet.Append(changeSet.Entries)

		if changeSet != nil && len(changeSet.Entries) > 0 {
			log.Info("server-side apply completed", "output", changeSet.ToMap())
			for _, change := range changeSet.Entries {
				if change.Action != string(ssa.UnchangedAction) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}
		}

		if err := manager.Wait(stageOne, ssa.WaitOptions{
			Interval: 2 * time.Second,
			Timeout:  kustomization.GetTimeout(),
		}); err != nil {
			return false, nil, err
		}
	}

	// sort by kind, validate and apply all the others objects
	sort.Sort(ssa.SortableUnstructureds(stageTwo))
	if len(stageTwo) > 0 {
		changeSet, err := manager.ApplyAll(ctx, stageTwo, applyOpts)
		if err != nil {
			return false, nil, fmt.Errorf("%w\n%s", err, changeSetLog.String())
		}
		resultSet.Append(changeSet.Entries)

		if changeSet != nil && len(changeSet.Entries) > 0 {
			log.Info("server-side apply completed", "output", changeSet.ToMap())
			for _, change := range changeSet.Entries {
				if change.Action != string(ssa.UnchangedAction) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}
		}
	}

	// emit event only if the server-side apply resulted in changes
	applyLog := strings.TrimSuffix(changeSetLog.String(), "\n")
	if applyLog != "" {
		r.event(ctx, kustomization, revision, events.EventSeverityInfo, applyLog, nil)
	}

	return applyLog != "", resultSet, nil
}

func (r *KustomizationReconciler) checkHealth(ctx context.Context, manager *ssa.ResourceManager, kustomization kustomizev1.Kustomization, revision string, drifted bool, objects object.ObjMetadataSet) error {
	if len(kustomization.Spec.HealthChecks) == 0 && !kustomization.Spec.Wait {
		return nil
	}

	checkStart := time.Now()
	var err error
	if !kustomization.Spec.Wait {
		objects, err = referenceToObjMetadataSet(kustomization.Spec.HealthChecks)
		if err != nil {
			return err
		}
	}

	if len(objects) == 0 {
		return nil
	}

	// guard against deadlock (waiting on itself)
	var toCheck []object.ObjMetadata
	for _, object := range objects {
		if object.GroupKind.Kind == kustomizev1.KustomizationKind &&
			object.Name == kustomization.GetName() &&
			object.Namespace == kustomization.GetNamespace() {
			continue
		}
		toCheck = append(toCheck, object)
	}

	// find the previous health check result
	wasHealthy := apimeta.IsStatusConditionTrue(kustomization.Status.Conditions, kustomizev1.HealthyCondition)

	// set the Healthy and Ready conditions to progressing
	message := fmt.Sprintf("running health checks with a timeout of %s", kustomization.GetTimeout().String())
	k := kustomizev1.KustomizationProgressing(kustomization, message)
	kustomizev1.SetKustomizationHealthiness(&k, metav1.ConditionUnknown, meta.ProgressingReason, message)
	if err := r.patchStatus(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&kustomization)}, k.Status); err != nil {
		return fmt.Errorf("unable to update the healthy status to progressing, error: %w", err)
	}

	// check the health with a default timeout of 30sec shorter than the reconciliation interval
	if err := manager.WaitForSet(toCheck, ssa.WaitOptions{
		Interval: 5 * time.Second,
		Timeout:  kustomization.GetTimeout(),
	}); err != nil {
		return fmt.Errorf("Health check failed after %s, %w", time.Since(checkStart).String(), err)
	}

	// emit event if the previous health check failed
	if !wasHealthy || (kustomization.Status.LastAppliedRevision != revision && drifted) {
		r.event(ctx, kustomization, revision, events.EventSeverityInfo,
			fmt.Sprintf("Health check passed in %s", time.Since(checkStart).String()), nil)
	}

	return nil
}

func (r *KustomizationReconciler) prune(ctx context.Context, manager *ssa.ResourceManager, kustomization kustomizev1.Kustomization, revision string, objects []*unstructured.Unstructured) (bool, error) {
	if !kustomization.Spec.Prune {
		return false, nil
	}

	log := ctrl.LoggerFrom(ctx)

	opts := ssa.DeleteOptions{
		PropagationPolicy: metav1.DeletePropagationBackground,
		Inclusions:        manager.GetOwnerLabels(kustomization.Name, kustomization.Namespace),
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
		r.event(ctx, kustomization, revision, events.EventSeverityInfo, changeSet.String(), nil)
		return true, nil
	}

	return false, nil
}

func (r *KustomizationReconciler) finalize(ctx context.Context, kustomization kustomizev1.Kustomization) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if kustomization.Spec.Prune &&
		!kustomization.Spec.Suspend &&
		kustomization.Status.Inventory != nil &&
		kustomization.Status.Inventory.Entries != nil {
		objects, _ := ListObjectsInInventory(kustomization.Status.Inventory)

		impersonation := NewKustomizeImpersonation(kustomization, r.Client, r.StatusPoller, "")
		kubeClient, _, err := impersonation.GetClient(ctx)
		if err != nil {
			// when impersonation fails, log the stale objects and continue with the finalization
			msg := fmt.Sprintf("unable to prune objects: \n%s", ssa.FmtUnstructuredList(objects))
			log.Error(fmt.Errorf("failed to build kube client: %w", err), msg)
			r.event(ctx, kustomization, kustomization.Status.LastAppliedRevision, events.EventSeverityError, msg, nil)
		} else {
			resourceManager := ssa.NewResourceManager(kubeClient, nil, ssa.Owner{
				Field: r.ControllerName,
				Group: kustomizev1.GroupVersion.Group,
			})

			opts := ssa.DeleteOptions{
				PropagationPolicy: metav1.DeletePropagationBackground,
				Inclusions:        resourceManager.GetOwnerLabels(kustomization.Name, kustomization.Namespace),
				Exclusions: map[string]string{
					fmt.Sprintf("%s/prune", kustomizev1.GroupVersion.Group):     kustomizev1.DisabledValue,
					fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
				},
			}

			changeSet, err := resourceManager.DeleteAll(ctx, objects, opts)
			if err != nil {
				r.event(ctx, kustomization, kustomization.Status.LastAppliedRevision, events.EventSeverityError, "pruning for deleted resource failed", nil)
				// Return the error so we retry the failed garbage collection
				return ctrl.Result{}, err
			}

			if changeSet != nil && len(changeSet.Entries) > 0 {
				r.event(ctx, kustomization, kustomization.Status.LastAppliedRevision, events.EventSeverityInfo, changeSet.String(), nil)
			}
		}
	}

	// Record deleted status
	r.recordReadiness(ctx, kustomization)

	// Remove our finalizer from the list and update it
	controllerutil.RemoveFinalizer(&kustomization, kustomizev1.KustomizationFinalizer)
	if err := r.Update(ctx, &kustomization); err != nil {
		return ctrl.Result{}, err
	}

	// Stop reconciliation as the object is being deleted
	return ctrl.Result{}, nil
}

func (r *KustomizationReconciler) event(ctx context.Context, kustomization kustomizev1.Kustomization, revision, severity, msg string, metadata map[string]string) {
	log := ctrl.LoggerFrom(ctx)

	annotations := map[string]string{
		kustomizev1.GroupVersion.Group + "/revision": revision,
	}

	eventtype := "Normal"
	if severity == events.EventSeverityError {
		eventtype = "Warning"
	}

	r.EventRecorder.AnnotatedEventf(&kustomization, annotations, eventtype, severity, msg)

	if r.ExternalEventRecorder != nil {
		objRef, err := reference.GetReference(r.Scheme, &kustomization)
		if err != nil {
			log.Error(err, "unable to send event")
			return
		}
		if metadata == nil {
			metadata = map[string]string{}
		}
		if revision != "" {
			metadata["revision"] = revision
		}

		reason := severity
		if c := apimeta.FindStatusCondition(kustomization.Status.Conditions, meta.ReadyCondition); c != nil {
			reason = c.Reason
		}

		if err := r.ExternalEventRecorder.Eventf(*objRef, metadata, severity, reason, msg); err != nil {
			log.Error(err, "unable to send event")
			return
		}
	}
}

func (r *KustomizationReconciler) recordReadiness(ctx context.Context, kustomization kustomizev1.Kustomization) {
	if r.MetricsRecorder == nil {
		return
	}
	log := ctrl.LoggerFrom(ctx)

	objRef, err := reference.GetReference(r.Scheme, &kustomization)
	if err != nil {
		log.Error(err, "unable to record readiness metric")
		return
	}
	if rc := apimeta.FindStatusCondition(kustomization.Status.Conditions, meta.ReadyCondition); rc != nil {
		r.MetricsRecorder.RecordCondition(*objRef, *rc, !kustomization.DeletionTimestamp.IsZero())
	} else {
		r.MetricsRecorder.RecordCondition(*objRef, metav1.Condition{
			Type:   meta.ReadyCondition,
			Status: metav1.ConditionUnknown,
		}, !kustomization.DeletionTimestamp.IsZero())
	}
}

func (r *KustomizationReconciler) recordSuspension(ctx context.Context, kustomization kustomizev1.Kustomization) {
	if r.MetricsRecorder == nil {
		return
	}
	log := ctrl.LoggerFrom(ctx)

	objRef, err := reference.GetReference(r.Scheme, &kustomization)
	if err != nil {
		log.Error(err, "unable to record suspended metric")
		return
	}

	if !kustomization.DeletionTimestamp.IsZero() {
		r.MetricsRecorder.RecordSuspend(*objRef, false)
	} else {
		r.MetricsRecorder.RecordSuspend(*objRef, kustomization.Spec.Suspend)
	}
}

func (r *KustomizationReconciler) patchStatus(ctx context.Context, req ctrl.Request, newStatus kustomizev1.KustomizationStatus) error {
	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return err
	}

	patch := client.MergeFrom(kustomization.DeepCopy())
	kustomization.Status = newStatus

	return r.Status().Patch(ctx, &kustomization, patch)
}
