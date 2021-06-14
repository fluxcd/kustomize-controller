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
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/metrics"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-retryablehttp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
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

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
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

func (r *KustomizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContext(ctx)
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
		return r.reconcileDelete(ctx, kustomization)
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
		log.Info("All dependencies area ready, proceeding with reconciliation")
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
	kustomization = kustomizev1.KustomizationProgressing(kustomization)
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
			time.Now().Sub(reconcileStart).String(),
			kustomization.GetRetryInterval().String()),
			"revision",
			source.GetArtifact().Revision)
		r.event(ctx, reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityError,
			reconcileErr.Error(), nil)
		return ctrl.Result{RequeueAfter: kustomization.GetRetryInterval()}, nil
	}

	// broadcast the reconciliation result and requeue at the specified interval
	log.Info(fmt.Sprintf("Reconciliation finished in %s, next run in %s",
		time.Now().Sub(reconcileStart).String(),
		kustomization.Spec.Interval.Duration.String()),
		"revision",
		source.GetArtifact().Revision,
	)
	r.event(ctx, reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityInfo,
		"Update completed", map[string]string{"commit_status": "update"})
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

	// create tmp dir
	tmpDir, err := ioutil.TempDir("", kustomization.Name)
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			sourcev1.StorageOperationFailedReason,
			err.Error(),
		), err
	}
	defer os.RemoveAll(tmpDir)

	// download artifact and extract files
	err = r.download(source.GetArtifact().URL, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, kustomization.Spec.Path)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// create any necessary kube-clients for impersonation
	impersonation := NewKustomizeImpersonation(kustomization, r.Client, r.StatusPoller, dirPath)
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), fmt.Errorf("failed to build kube client: %w", err)
	}

	// generate kustomization.yaml and calculate the manifests checksum
	checksum, err := r.generate(ctx, kubeClient, kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// build the kustomization and generate the GC snapshot
	snapshot, err := r.build(ctx, kustomization, checksum, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// dry-run apply
	err = r.validate(ctx, kustomization, impersonation, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ValidationFailedReason,
			err.Error(),
		), err
	}

	// apply
	changeSet, err := r.applyWithRetry(ctx, kustomization, impersonation, source.GetArtifact().Revision, dirPath, 5*time.Second)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), err
	}

	// prune
	err = r.prune(ctx, kubeClient, kustomization, checksum)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.PruneFailedReason,
			err.Error(),
		), err
	}

	// health assessment
	err = r.checkHealth(ctx, statusPoller, kustomization, source.GetArtifact().Revision, changeSet != "")
	if err != nil {
		return kustomizev1.KustomizationNotReadySnapshot(
			kustomization,
			snapshot,
			source.GetArtifact().Revision,
			kustomizev1.HealthCheckFailedReason,
			err.Error(),
		), err
	}

	return kustomizev1.KustomizationReady(
		kustomization,
		snapshot,
		source.GetArtifact().Revision,
		meta.ReconciliationSucceededReason,
		"Applied revision: "+source.GetArtifact().Revision,
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

func (r *KustomizationReconciler) generate(ctx context.Context, kubeClient client.Client, kustomization kustomizev1.Kustomization, dirPath string) (string, error) {
	gen := NewGenerator(kustomization, kubeClient)
	return gen.WriteFile(ctx, dirPath)
}

func (r *KustomizationReconciler) build(ctx context.Context, kustomization kustomizev1.Kustomization, checksum, dirPath string) (*kustomizev1.Snapshot, error) {
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

func (r *KustomizationReconciler) validate(ctx context.Context, kustomization kustomizev1.Kustomization, imp *KustomizeImpersonation, dirPath string) error {
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
					r.event(ctx, kustomization, revision, events.EventSeverityInfo, changeSet, nil)
				}
			}
		} else {
			return "", err
		}
	} else {
		if changeSet != "" && kustomization.Status.LastAppliedRevision != revision {
			r.event(ctx, kustomization, revision, events.EventSeverityInfo, changeSet, nil)
		}
	}
	return changeSet, nil
}

func (r *KustomizationReconciler) prune(ctx context.Context, kubeClient client.Client, kustomization kustomizev1.Kustomization, newChecksum string) error {
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
			r.event(ctx, kustomization, newChecksum, events.EventSeverityInfo, output, nil)
		}
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(ctx context.Context, statusPoller *polling.StatusPoller, kustomization kustomizev1.Kustomization, revision string, changed bool) error {
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
		r.event(ctx, kustomization, revision, events.EventSeverityInfo, "Health check passed", nil)
	}
	return nil
}

func (r *KustomizationReconciler) reconcileDelete(ctx context.Context, kustomization kustomizev1.Kustomization) (ctrl.Result, error) {
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
			r.event(ctx, kustomization, kustomization.Status.LastAppliedRevision, events.EventSeverityError, "pruning for deleted resource failed", nil)
			// Return the error so we retry the failed garbage collection
			return ctrl.Result{}, err
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
	log := logr.FromContext(ctx)
	r.EventRecorder.Event(&kustomization, "Normal", severity, msg)
	objRef, err := reference.GetReference(r.Scheme, &kustomization)
	if err != nil {
		log.Error(err, "unable to send event")
		return
	}

	if r.ExternalEventRecorder != nil {
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
	log := logr.FromContext(ctx)

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
	log := logr.FromContext(ctx)

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
