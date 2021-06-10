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
	"context"
	"errors"
	"fmt"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/patch"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-retryablehttp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/kustomize/api/filesys"

	"github.com/fluxcd/pkg/apis/meta"
	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/finalizers,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets;gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets/status;gitrepositories/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	httpClient        *retryablehttp.Client
	requeueDependency time.Duration
	helper.Events
	helper.Metrics
	StatusPoller *polling.StatusPoller
}

type KustomizationReconcilerOptions struct {
	MaxConcurrentReconciles   int
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	// Index the Kustomizations by the GitRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(context.TODO(), &kustomizev1.Kustomization{}, kustomizev1.GitRepositoryIndexKey,
		r.indexBy(sourcev1.GitRepositoryKind)); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	// Index the Kustomizations by the Bucket references they (may) point at.
	if err := mgr.GetCache().IndexField(context.TODO(), &kustomizev1.Kustomization{}, kustomizev1.BucketIndexKey,
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
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(kustomizev1.GitRepositoryIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&source.Kind{Type: &sourcev1.Bucket{}},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(kustomizev1.BucketIndexKey)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *KustomizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := logr.FromContext(ctx)
	reconcileStart := time.Now()

	kustomization := &kustomizev1.Kustomization{}
	if err := r.Get(ctx, req.NamespacedName, kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Record suspended status metric
	defer r.RecordSuspend(ctx, kustomization, kustomization.Spec.Suspend)

	// Return early if the Kustomization is suspended.
	if kustomization.Spec.Suspend {
		log.Info("Reconciliation is suspended for this object")
		return ctrl.Result{}, nil
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(kustomization, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		// record the value of the reconciliation request, if any
		if v, ok := meta.ReconcileAnnotationValue(kustomization.GetAnnotations()); ok {
			kustomization.Status.SetLastHandledReconcileRequest(v)
		}

		// Summarize Ready condition
		conditions.SetSummary(kustomization,
			meta.ReadyCondition,
			conditions.WithConditions(
				sourcev1.ArtifactAvailableCondition,
				sourcev1.SourceAvailableCondition,
			),
		)

		// TODO(somtochiama):
		// Patch the object, ignoring conflicts on the conditions owned by
		// this controller
		patchOpts := []patch.Option{
			patch.WithOwnedConditions{
				Conditions: []string{
					sourcev1.ArtifactAvailableCondition,
					sourcev1.SourceAvailableCondition,
					meta.ReadyCondition,
					meta.ReconcilingCondition,
					meta.StalledCondition,
					meta.ProgressingReason,
				},
			},
		}

		// Determine if the resource is still being reconciled, or if
		// it has stalled, and record this observation
		if retErr == nil {

			if result.IsZero() || !result.Requeue {
				// We are no longer reconciling
				conditions.Delete(kustomization, meta.ReconcilingCondition)

				// We have now observed this generation
				patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})

				readyCondition := conditions.Get(kustomization, meta.ReadyCondition)
				switch readyCondition.Status {
				case metav1.ConditionFalse:
					// As we are no longer reconciling and the end-state
					// is not ready, the reconciliation has stalled
					conditions.MarkTrue(kustomization, meta.StalledCondition, readyCondition.Reason, readyCondition.Message)
				case metav1.ConditionTrue:
					// As we are no longer reconciling and the end-state
					// is ready, the reconciliation is no longer stalled
					conditions.Delete(kustomization, meta.StalledCondition)
				}
			}
		}

		// Finally, patch the resource
		if err := patchHelper.Patch(ctx, kustomization, patchOpts...); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}

		// Always record readiness and duration metrics
		r.Metrics.RecordReadinessMetric(ctx, kustomization)
		r.Metrics.RecordDuration(ctx, kustomization, reconcileStart)
	}()

	// Add our finalizer if it does not exist
	if !controllerutil.ContainsFinalizer(kustomization, kustomizev1.KustomizationFinalizer) {
		controllerutil.AddFinalizer(kustomization, kustomizev1.KustomizationFinalizer)
		if err := r.Update(ctx, kustomization); err != nil {
			log.Error(err, "unable to register finalizer")
			return ctrl.Result{}, err
		}
	}

	// Examine if the object is under deletion
	if !kustomization.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, kustomization)
	}

	return r.reconcile(ctx, kustomization)
}

func (r *KustomizationReconciler) reconcile(
	ctx context.Context,
	kustomization *kustomizev1.Kustomization) (ctrl.Result, error) {
	// Mark the resource as under reconciliation
	conditions.MarkTrue(kustomization, meta.ReconcilingCondition, "Reconciling", "")

	var artifact sourcev1.Artifact
	if result, err := r.reconcileSource(ctx, kustomization, &artifact); err != nil || conditions.IsFalse(kustomization, sourcev1.SourceAvailableCondition) {
		return result, err
	}

	if result, err := r.reconcileDependencies(ctx, kustomization, artifact.Revision); err != nil {
		return result, err
	}

	// Mark the resource as under reconciliation
	conditions.MarkTrue(kustomization, meta.ReconcilingCondition, "Reconciling", "")
	kustomization.Status.LastAttemptedRevision = artifact.Revision

	// create tmp dir
	tmpDir, err := ioutil.TempDir("", kustomization.Name)
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		conditions.MarkFalse(kustomization, sourcev1.ArtifactAvailableCondition, sourcev1.StorageOperationFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}
	defer os.RemoveAll(tmpDir)

	// download artifact and extract files
	err = r.download(artifact.URL, tmpDir)
	if err != nil {
		conditions.MarkFalse(kustomization, sourcev1.ArtifactAvailableCondition, kustomizev1.ArtifactFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, kustomization.Spec.Path)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.ArtifactFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.ArtifactFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// create any necessary kube-clients for impersonation
	impersonation := NewKustomizeImpersonation(kustomization, r.Client, r.StatusPoller, dirPath)
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, meta.ReconciliationFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, fmt.Errorf("failed to build kube client: %w", err)
	}

	// generate kustomization.yaml and calculate the manifests checksum
	checksum, err := r.generate(ctx, kubeClient, kustomization, dirPath)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.BuildFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// build the kustomization and generate the GC snapshot
	snapshot, err := r.build(ctx, kustomization, checksum, dirPath)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.BuildFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// dry-run apply
	err = r.validate(ctx, kustomization, impersonation, dirPath)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.ValidationFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// apply
	changeSet, err := r.applyWithRetry(ctx, *kustomization, impersonation, artifact.Revision, dirPath, 5*time.Second)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, meta.ReconciliationFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// prune
	err = r.prune(ctx, kubeClient, kustomization, checksum)
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.PruneFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	// health assessment
	err = r.checkHealth(ctx, statusPoller, kustomization, artifact.Revision, changeSet != "")
	if err != nil {
		conditions.MarkFalse(kustomization, meta.ReadyCondition, kustomizev1.HealthCheckFailedReason, err.Error())
		conditions.MarkFalse(kustomization, kustomizev1.HealthyCondition, kustomizev1.HealthCheckFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, err
	}

	kustomization.Status.LastAppliedRevision = artifact.Revision
	kustomization.Status.Snapshot = snapshot
	msg := "Applied revision: " + artifact.Revision
	conditions.MarkTrue(kustomization, meta.ReadyCondition, meta.ReconciliationSucceededReason, msg)
	conditions.MarkTrue(kustomization, kustomizev1.HealthyCondition, meta.ReconciliationSucceededReason, msg)
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

func (r *KustomizationReconciler) reconcileSource(ctx context.Context, kustomization *kustomizev1.Kustomization, artifact *sourcev1.Artifact) (ctrl.Result, error) {
	// resolve source reference
	sourceObj, err := r.getSource(ctx, *kustomization)
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			msg := fmt.Sprintf("Source '%s' not found", kustomization.Spec.SourceRef.String())
			conditions.MarkFalse(kustomization, sourcev1.SourceAvailableCondition, kustomizev1.ArtifactFailedReason, msg)
			logr.FromContext(ctx).Info(msg)
			return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
		default:
			return ctrl.Result{}, err
		}
	}

	// Mirror source readiness
	conditions.SetMirror(kustomization, sourcev1.SourceRefReadyCondition, sourceObj)

	// Confirm source has an artifact
	if sourceObj.GetArtifact() == nil {
		conditions.MarkFalse(kustomization,
			sourcev1.SourceAvailableCondition,
			"NoArtifact",
			"No artifact available for %s %q",
			kustomization.Spec.SourceRef.Kind,
			kustomization.Spec.SourceRef.Name)
		// The watcher should notice an artifact change
		return ctrl.Result{}, nil
	}

	*artifact = *sourceObj.GetArtifact()
	return ctrl.Result{}, nil
}

func (r *KustomizationReconciler) reconcileDependencies(ctx context.Context, kustomization *kustomizev1.Kustomization, revision string) (ctrl.Result, error) {
	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(*kustomization); err != nil {
			conditions.MarkFalse(kustomization, meta.ReadyCondition, meta.DependencyNotReadyReason, err.Error())
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue on a fix interval.
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			logr.FromContext(ctx).Info(msg)
			r.Events.Event(ctx, kustomization, map[string]string{
				"revision": revision,
			}, events.EventSeverityInfo, "DependencyNotReady", msg)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}

		logr.FromContext(ctx).Info("All dependencies area ready, proceeding with reconciliation")
	}

	return ctrl.Result{}, nil
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

func (r *KustomizationReconciler) download(artifactURL string, tmpDir string) error {
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

	// extract
	if _, err = untar.Untar(resp.Body, tmpDir); err != nil {
		return fmt.Errorf("failed to untar artifact, error: %w", err)
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

func (r *KustomizationReconciler) generate(ctx context.Context, kubeClient client.Client, kustomization *kustomizev1.Kustomization, dirPath string) (string, error) {
	gen := NewGenerator(kustomization, kubeClient)
	return gen.WriteFile(ctx, dirPath)
}

func (r *KustomizationReconciler) build(ctx context.Context, kustomization *kustomizev1.Kustomization, checksum, dirPath string) (*kustomizev1.Snapshot, error) {
	timeout := kustomization.GetTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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

	manifestsFile := filepath.Join(dirPath, fmt.Sprintf("%s.yaml", kustomization.GetUID()))
	if err := fs.WriteFile(manifestsFile, resources); err != nil {
		return nil, err
	}

	return kustomizev1.NewSnapshot(resources, checksum)
}

func (r *KustomizationReconciler) validate(ctx context.Context, kustomization *kustomizev1.Kustomization, imp *KustomizeImpersonation, dirPath string) error {
	if kustomization.Spec.Validation == "" || kustomization.Spec.Validation == "none" {
		return nil
	}

	log := logr.FromContext(ctx)
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	applyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	validation := kustomization.Spec.Validation
	if validation == "server" && kustomization.Spec.Force {
		// Use client-side validation with force
		validation = "client"
		log.Info(fmt.Sprintf("Server-side validation is configured, falling-back to client-side validation since 'force' is enabled"))
	}

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --dry-run=%s --cache-dir=/tmp --force=%t",
		dirPath, kustomization.GetUID(), kustomization.GetTimeout().String(), validation, kustomization.Spec.Force)

	if kustomization.Spec.KubeConfig != nil {
		kubeConfig, err := imp.WriteKubeConfig(ctx)
		if err != nil {
			return err
		}
		cmd = fmt.Sprintf("%s --kubeconfig=%s", cmd, kubeConfig)
	} else {
		// impersonate SA
		if kustomization.Spec.ServiceAccountName != "" {
			saToken, err := imp.GetServiceAccountToken(ctx)
			if err != nil {
				return fmt.Errorf("service account impersonation failed: %w", err)
			}

			cmd = fmt.Sprintf("%s --token %s", cmd, saToken)
		}
	}

	command := exec.CommandContext(applyCtx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(applyCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("validation timeout: %w", applyCtx.Err())
		}
		return fmt.Errorf("validation failed: %s", parseApplyError(output))
	}
	return nil
}

func (r *KustomizationReconciler) apply(ctx context.Context, kustomization kustomizev1.Kustomization, imp *KustomizeImpersonation, dirPath string) (string, error) {
	log := logr.FromContext(ctx)
	start := time.Now()
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	applyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fieldManager := "kustomize-controller"

	cmd := fmt.Sprintf("cd %s && kubectl apply --field-manager=%s -f %s.yaml --timeout=%s --cache-dir=/tmp --force=%t",
		dirPath, fieldManager, kustomization.GetUID(), kustomization.Spec.Interval.Duration.String(), kustomization.Spec.Force)

	if kustomization.Spec.KubeConfig != nil {
		kubeConfig, err := imp.WriteKubeConfig(ctx)
		if err != nil {
			return "", err
		}
		cmd = fmt.Sprintf("%s --kubeconfig=%s", cmd, kubeConfig)
	} else {
		// impersonate SA
		if kustomization.Spec.ServiceAccountName != "" {
			saToken, err := imp.GetServiceAccountToken(ctx)
			if err != nil {
				return "", fmt.Errorf("service account impersonation failed: %w", err)
			}

			cmd = fmt.Sprintf("%s --token %s", cmd, saToken)
		}
	}

	command := exec.CommandContext(applyCtx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(applyCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("apply timeout: %w", applyCtx.Err())
		}

		if string(output) == "" {
			return "", fmt.Errorf("apply failed: %w, kubectl process was killed, probably due to OOM", err)
		}

		applyErr := parseApplyError(output)
		if applyErr == "" {
			applyErr = "no error output found, this may happen because of a timeout"
		}
		return "", fmt.Errorf("apply failed: %s", applyErr)
	}

	resources := parseApplyOutput(output)
	log.Info(
		fmt.Sprintf("Kustomization applied in %s",
			time.Now().Sub(start).String()),
		"output", resources,
	)

	changeSet := ""
	for obj, action := range resources {
		if action != "" && action != "unchanged" {
			changeSet += obj + " " + action + "\n"
		}
	}
	return changeSet, nil
}

func (r *KustomizationReconciler) applyWithRetry(ctx context.Context, kustomization kustomizev1.Kustomization, imp *KustomizeImpersonation, revision, dirPath string, delay time.Duration) (string, error) {
	log := logr.FromContext(ctx)
	changeSet, err := r.apply(ctx, kustomization, imp, dirPath)
	if err != nil {
		// retry apply due to CRD/CR race
		if strings.Contains(err.Error(), "could not find the requested resource") ||
			strings.Contains(err.Error(), "no matches for kind") {
			log.Info("retrying apply", "error", err.Error())
			time.Sleep(delay)
			if changeSet, err := r.apply(ctx, kustomization, imp, dirPath); err != nil {
				return "", err
			} else {
				if changeSet != "" {
					r.Events.Event(ctx, &kustomization, map[string]string{
						"revision": revision,
					}, events.EventSeverityInfo, "KustomizationApplied", changeSet)
				}
			}
		} else {
			return "", err
		}
	} else {
		if changeSet != "" && kustomization.Status.LastAppliedRevision != revision {
			r.Events.Event(ctx, &kustomization, map[string]string{
				"revision": revision,
			}, events.EventSeverityInfo, "KustomizationApplied", changeSet)
		}
	}
	return changeSet, nil
}

func (r *KustomizationReconciler) prune(ctx context.Context, kubeClient client.Client, kustomization *kustomizev1.Kustomization, newChecksum string) error {
	if !kustomization.Spec.Prune || kustomization.Status.Snapshot == nil {
		return nil
	}
	if kustomization.DeletionTimestamp.IsZero() && kustomization.Status.Snapshot.Checksum == newChecksum {
		return nil
	}

	log := logr.FromContext(ctx)
	gc := NewGarbageCollector(kubeClient, *kustomization.Status.Snapshot, newChecksum, logr.FromContext(ctx))

	if output, ok := gc.Prune(kustomization.GetTimeout(),
		kustomization.GetName(),
		kustomization.GetNamespace(),
	); !ok {
		return fmt.Errorf("garbage collection failed: %s", output)
	} else {
		if output != "" {
			log.Info(fmt.Sprintf("garbage collection completed: %s", output))
			r.Events.Event(ctx, kustomization, map[string]string{
				"revision": newChecksum,
			}, events.EventSeverityInfo, "GarbageCollectionCompleted", output)
		}
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(ctx context.Context, statusPoller *polling.StatusPoller, kustomization *kustomizev1.Kustomization, revision string, changed bool) error {
	if len(kustomization.Spec.HealthChecks) == 0 {
		return nil
	}

	hc := NewHealthCheck(kustomization, statusPoller)

	if err := hc.Assess(1 * time.Second); err != nil {
		return err
	}

	healthiness := apimeta.FindStatusCondition(kustomization.Status.Conditions, kustomizev1.HealthyCondition)
	healthy := healthiness != nil && healthiness.Status == metav1.ConditionTrue

	if !healthy || (kustomization.Status.LastAppliedRevision != revision && changed) {
		r.Events.Event(ctx, kustomization, map[string]string{
			"revision": revision,
		}, events.EventSeverityInfo, "HealthCheckPassed", "Health check passed")
	}

	return nil
}

func (r *KustomizationReconciler) reconcileDelete(ctx context.Context, kustomization *kustomizev1.Kustomization) (ctrl.Result, error) {
	log := logr.FromContext(ctx)
	if kustomization.Spec.Prune && !kustomization.Spec.Suspend {
		// create any necessary kube-clients
		imp := NewKustomizeImpersonation(kustomization, r.Client, r.StatusPoller, "")
		client, _, err := imp.GetClient(ctx)
		if err != nil {
			err = fmt.Errorf("failed to build kube client for Kustomization: %w", err)
			log.Error(err, "Unable to prune for finalizer")
			return ctrl.Result{}, err
		}
		if err := r.prune(ctx, client, kustomization, ""); err != nil {
			r.Events.Event(ctx, kustomization, map[string]string{
				"revision": kustomization.Status.LastAppliedRevision,
			}, events.EventSeverityError, "GarbageCollectionFailed", "pruning for deleted resource failed")
			// Return the error so we retry the failed garbage collection
			return ctrl.Result{}, err
		}
	}

	// Remove our finalizer from the list and update it
	controllerutil.RemoveFinalizer(kustomization, kustomizev1.KustomizationFinalizer)

	// Stop reconciliation as the object is being deleted
	return ctrl.Result{}, nil
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
