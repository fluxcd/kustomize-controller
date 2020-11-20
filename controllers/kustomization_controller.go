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
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/krusty"
	kustypes "sigs.k8s.io/kustomize/api/types"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/dependency"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/metrics"
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
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	requeueDependency     time.Duration
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EventRecorder         kuberecorder.EventRecorder
	ExternalEventRecorder *events.Recorder
	MetricsRecorder       *metrics.Recorder
	StatusPoller          *polling.StatusPoller
}

type KustomizationReconcilerOptions struct {
	MaxConcurrentReconciles   int
	DependencyRequeueInterval time.Duration
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	// Index the Kustomizations by the GitRepository references they (may) point at.
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &kustomizev1.Kustomization{}, kustomizev1.SourceIndexKey,
		func(rawObj runtime.Object) []string {
			k := rawObj.(*kustomizev1.Kustomization)
			if k.Spec.SourceRef.Kind == sourcev1.GitRepositoryKind {
				namespace := k.GetNamespace()
				if k.Spec.SourceRef.Namespace != "" {
					namespace = k.Spec.SourceRef.Namespace
				}
				return []string{fmt.Sprintf("%s/%s", namespace, k.Spec.SourceRef.Name)}
			}
			return nil
		},
	); err != nil {
		return err
	}
	// Index the Kustomizations by the Bucket references they (may) point at.
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &kustomizev1.Kustomization{}, kustomizev1.BucketIndexKey,
		func(rawObj runtime.Object) []string {
			k := rawObj.(*kustomizev1.Kustomization)
			if k.Spec.SourceRef.Kind == sourcev1.BucketKind {
				namespace := k.GetNamespace()
				if k.Spec.SourceRef.Namespace != "" {
					namespace = k.Spec.SourceRef.Namespace
				}
				return []string{fmt.Sprintf("%s/%s", namespace, k.Spec.SourceRef.Name)}
			}
			return nil
		},
	); err != nil {
		return err
	}

	r.requeueDependency = opts.DependencyRequeueInterval
	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}, builder.WithPredicates(predicates.ChangePredicate{})).
		Watches(
			&source.Kind{Type: &sourcev1.GitRepository{}},
			&handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(r.requestsForGitRepositoryRevisionChange)},
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&source.Kind{Type: &sourcev1.Bucket{}},
			&handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(r.requestsForBucketRevisionChange)},
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *KustomizationReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	reconcileStart := time.Now()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues("controller", strings.ToLower(kustomizev1.KustomizationKind), "request", req.NamespacedName)

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
		return r.reconcileDelete(ctx, log, kustomization)
	}

	if kustomization.Spec.Suspend {
		msg := "Kustomization is suspended, skipping reconciliation"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", meta.SuspendedReason, msg)
		if err := r.updateStatus(ctx, req, kustomization.Status); err != nil {
			log.Error(err, "unable to update status")
			return ctrl.Result{Requeue: true}, err
		}
		r.recordReadiness(kustomization)
		log.Info(msg)
		return ctrl.Result{}, nil
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
	if err := r.updateStatus(ctx, req, kustomization.Status); err != nil {
		log.Error(err, "unable to update status to progressing")
		return ctrl.Result{Requeue: true}, err
	}
	r.recordReadiness(kustomization)

	// resolve source reference
	source, err := r.getSource(ctx, kustomization)
	if err != nil {
		if apierrors.IsNotFound(err) {
			msg := "Source not found"
			kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.ArtifactFailedReason, msg)
			if err := r.updateStatus(ctx, req, kustomization.Status); err != nil {
				log.Error(err, "unable to update status for source not found")
				return ctrl.Result{Requeue: true}, err
			}
			r.recordReadiness(kustomization)
			log.Info(msg)
			// do not requeue, when the source is created the watcher should trigger a reconciliation
			return ctrl.Result{}, nil
		} else {
			// retry on transient errors
			return ctrl.Result{Requeue: true}, err
		}
	}

	if source.GetArtifact() == nil {
		msg := "Source is not ready, artifact not found"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.ArtifactFailedReason, msg)
		if err := r.updateStatus(ctx, req, kustomization.Status); err != nil {
			log.Error(err, "unable to update status for artifact not found")
			return ctrl.Result{Requeue: true}, err
		}
		r.recordReadiness(kustomization)
		log.Info(msg)
		// do not requeue, when the artifact is created the watcher should trigger a reconciliation
		return ctrl.Result{}, nil
	}

	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(kustomization); err != nil {
			kustomization = kustomizev1.KustomizationNotReady(
				kustomization, source.GetArtifact().Revision, meta.DependencyNotReadyReason, err.Error())
			if err := r.updateStatus(ctx, req, kustomization.Status); err != nil {
				log.Error(err, "unable to update status for dependency not ready")
				return ctrl.Result{Requeue: true}, err
			}
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue on a fix interval.
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			log.Error(err, msg)
			r.event(kustomization, source.GetArtifact().Revision, events.EventSeverityInfo, msg, nil)
			r.recordReadiness(kustomization)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}
		log.Info("All dependencies area ready, proceeding with reconciliation")
	}

	// reconcile kustomization by applying the latest revision
	reconciledKustomization, reconcileErr := r.reconcile(*kustomization.DeepCopy(), source)
	if reconcileErr != nil {
		// broadcast the error
		r.event(reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityError, reconcileErr.Error(), nil)
	}

	// update status
	if err := r.updateStatus(ctx, req, reconciledKustomization.Status); err != nil {
		log.Error(err, "unable to update status after reconciliation")
		return ctrl.Result{Requeue: true}, err
	}

	log.Info(fmt.Sprintf("Reconciliation finished in %s, next run in %s",
		time.Now().Sub(reconcileStart).String(),
		kustomization.Spec.Interval.Duration.String()),
		"revision",
		source.GetArtifact().Revision,
	)

	// requeue
	if reconcileErr != nil {
		// record the reconciliation error
		r.recordReadiness(reconciledKustomization)
		return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, reconcileErr
	}

	// record the reconciliation result
	r.event(reconciledKustomization, source.GetArtifact().Revision, events.EventSeverityInfo,
		"Update completed", map[string]string{"commit_status": "update"})
	r.recordReadiness(reconciledKustomization)
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

func (r *KustomizationReconciler) reconcile(
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
	err = r.download(kustomization, source.GetArtifact().URL, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	dirPath := path.Join(tmpDir, kustomization.Spec.Path)
	// check build path exists
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// generate kustomization.yaml and calculate the manifests checksum
	checksum, err := r.generate(kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// build the kustomization and generate the GC snapshot
	snapshot, err := r.build(kustomization, checksum, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), err
	}

	// dry-run apply
	err = r.validate(kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ValidationFailedReason,
			err.Error(),
		), err
	}

	// apply
	changeSet, err := r.applyWithRetry(kustomization, source.GetArtifact().Revision, dirPath, 5*time.Second)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), err
	}

	// create any necessary kube-clients
	client, statusPoller, err := r.newKustomizationClient(kustomization)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), fmt.Errorf("Failed to build kube client for Kustomization: %w", err)
	}

	// prune
	err = r.prune(client, kustomization, snapshot)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.PruneFailedReason,
			err.Error(),
		), err
	}

	// health assessment
	err = r.checkHealth(statusPoller, kustomization, source.GetArtifact().Revision, changeSet != "")
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

func (r *KustomizationReconciler) download(kustomization kustomizev1.Kustomization, url string, tmpDir string) error {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// download the tarball
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request for %s, error: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to download artifact from %s, error: %w", url, err)
	}
	defer resp.Body.Close()

	// check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("faild to download artifact from %s, status: %s", url, resp.Status)
	}

	// extract
	if _, err = untar.Untar(resp.Body, tmpDir); err != nil {
		return fmt.Errorf("faild to untar artifact, error: %w", err)
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

func (r *KustomizationReconciler) generate(kustomization kustomizev1.Kustomization, dirPath string) (string, error) {
	gen := NewGenerator(kustomization)
	return gen.WriteFile(dirPath)
}

func (r *KustomizationReconciler) build(kustomization kustomizev1.Kustomization, checksum, dirPath string) (*kustomizev1.Snapshot, error) {
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
	manifestsFile := filepath.Join(dirPath, fmt.Sprintf("%s.yaml", kustomization.GetUID()))

	opt := krusty.MakeDefaultOptions()
	opt.LoadRestrictions = kustypes.LoadRestrictionsNone
	opt.DoLegacyResourceSort = true
	k := krusty.MakeKustomizer(fs, opt)
	m, err := k.Run(dirPath)
	if err != nil {
		return nil, err
	}

	// check if resources are encrypted and decrypt them before generating the final YAML
	if kustomization.Spec.Decryption != nil {
		for _, res := range m.Resources() {
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
	}

	resources, err := m.AsYaml()
	if err != nil {
		return nil, err
	}

	if err := fs.WriteFile(manifestsFile, resources); err != nil {
		return nil, err
	}

	return kustomizev1.NewSnapshot(resources, checksum)
}

func (r *KustomizationReconciler) reconcileDelete(ctx context.Context, log logr.Logger, kustomization kustomizev1.Kustomization) (ctrl.Result, error) {
	if kustomization.Spec.Prune && !kustomization.Spec.Suspend {
		// create any necessary kube-clients
		client, _, err := r.newKustomizationClient(kustomization)
		if err != nil {
			err = fmt.Errorf("failed to build kube client for Kustomization: %w", err)
			log.Error(err, "Unable to prune for finalizer")
			return ctrl.Result{}, err
		}
		if err := r.prune(client, kustomization, kustomization.Status.Snapshot); err != nil {
			r.event(kustomization, kustomization.Status.LastAppliedRevision, events.EventSeverityError, "pruning for deleted resource failed", nil)
			// Return the error so we retry the failed garbage collection
			return ctrl.Result{}, err
		}
	}

	// Record deleted status
	r.recordReadiness(kustomization)

	// Remove our finalizer from the list and update it
	controllerutil.RemoveFinalizer(&kustomization, kustomizev1.KustomizationFinalizer)
	if err := r.Update(ctx, &kustomization); err != nil {
		return ctrl.Result{}, err
	}

	// Stop reconciliation as the object is being deleted
	return ctrl.Result{}, nil
}

func (r *KustomizationReconciler) validate(kustomization kustomizev1.Kustomization, dirPath string) error {
	if kustomization.Spec.Validation == "" || kustomization.Spec.Validation == "none" {
		return nil
	}

	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --dry-run=%s --cache-dir=/tmp",
		dirPath, kustomization.GetUID(), kustomization.GetTimeout().String(), kustomization.Spec.Validation)

	if kustomization.Spec.KubeConfig != nil {
		kubeConfig, err := r.writeKubeConfig(kustomization, dirPath)
		if err != nil {
			return err
		}
		cmd = fmt.Sprintf("%s --kubeconfig=%s", cmd, kubeConfig)
	}

	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("validation timeout: %w", err)
		}
		return fmt.Errorf("validation failed: %s", string(output))
	}
	return nil
}

func (r *KustomizationReconciler) writeKubeConfig(kustomization kustomizev1.Kustomization, dirPath string) (string, error) {
	secretName := types.NamespacedName{
		Namespace: kustomization.GetNamespace(),
		Name:      kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	kubeConfig, err := r.getKubeConfig(kustomization)
	if err != nil {
		return "", err
	}

	kubeConfigPath := path.Join(dirPath, secretName.Name)
	if err := ioutil.WriteFile(kubeConfigPath, kubeConfig, os.ModePerm); err != nil {
		return "", fmt.Errorf("unable to write KubeConfig secret '%s' to storage: %w", secretName.String(), err)
	}

	return secretName.Name, nil
}

func (r *KustomizationReconciler) getKubeConfig(kustomization kustomizev1.Kustomization) ([]byte, error) {
	timeout := kustomization.GetTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	secretName := types.NamespacedName{
		Namespace: kustomization.GetNamespace(),
		Name:      kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	var secret corev1.Secret
	if err := r.Get(ctx, secretName, &secret); err != nil {
		return nil, fmt.Errorf("unable to read KubeConfig secret '%s' error: %w", secretName.String(), err)
	}

	kubeConfig, ok := secret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("KubeConfig secret '%s' doesn't contain a 'value' key ", secretName.String())
	}

	return kubeConfig, nil
}

func (r *KustomizationReconciler) getServiceAccountToken(kustomization kustomizev1.Kustomization) (string, error) {
	namespacedName := types.NamespacedName{
		Namespace: kustomization.Namespace,
		Name:      kustomization.Spec.ServiceAccountName,
	}

	var serviceAccount corev1.ServiceAccount
	err := r.Client.Get(context.TODO(), namespacedName, &serviceAccount)
	if err != nil {
		return "", err
	}

	secretName := types.NamespacedName{
		Namespace: kustomization.Namespace,
		Name:      kustomization.Spec.ServiceAccountName,
	}

	for _, secret := range serviceAccount.Secrets {
		if strings.HasPrefix(secret.Name, fmt.Sprintf("%s-token", serviceAccount.Name)) {
			secretName.Name = secret.Name
			break
		}
	}

	var secret corev1.Secret
	err = r.Client.Get(context.TODO(), secretName, &secret)
	if err != nil {
		return "", err
	}

	var token string
	if data, ok := secret.Data["token"]; ok {
		token = string(data)
	} else {
		return "", fmt.Errorf("the service account secret '%s' does not containt a token", secretName.String())
	}

	return token, nil
}

func (r *KustomizationReconciler) apply(kustomization kustomizev1.Kustomization, dirPath string) (string, error) {
	start := time.Now()
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --cache-dir=/tmp",
		dirPath, kustomization.GetUID(), kustomization.Spec.Interval.Duration.String())

	if kustomization.Spec.KubeConfig != nil {
		kubeConfig, err := r.writeKubeConfig(kustomization, dirPath)
		if err != nil {
			return "", err
		}
		cmd = fmt.Sprintf("%s --kubeconfig=%s", cmd, kubeConfig)
	} else {
		// impersonate SA
		if kustomization.Spec.ServiceAccountName != "" {
			saToken, err := r.getServiceAccountToken(kustomization)
			if err != nil {
				return "", fmt.Errorf("service account impersonation failed: %w", err)
			}

			cmd = fmt.Sprintf("%s --token %s", cmd, saToken)
		}
	}

	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("apply timeout: %w", err)
		}
		return "", fmt.Errorf("apply failed: %s", string(output))
	}

	resources := r.parseApplyOutput(output)
	r.Log.WithValues(
		strings.ToLower(kustomization.Kind),
		fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
	).Info(
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

func (r *KustomizationReconciler) applyWithRetry(kustomization kustomizev1.Kustomization, revision, dirPath string, delay time.Duration) (string, error) {
	changeSet, err := r.apply(kustomization, dirPath)
	if err != nil {
		// retry apply due to CRD/CR race
		if strings.Contains(err.Error(), "could not find the requested resource") ||
			strings.Contains(err.Error(), "no matches for kind") {
			r.Log.Info("retrying apply",
				"error", err.Error(),
				"kustomization", fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()))
			time.Sleep(delay)
			if changeSet, err := r.apply(kustomization, dirPath); err != nil {
				return "", err
			} else {
				if changeSet != "" {
					r.event(kustomization, revision, events.EventSeverityInfo, changeSet, nil)
				}
			}
		} else {
			return "", err
		}
	} else {
		if changeSet != "" && kustomization.Status.LastAppliedRevision != revision {
			r.event(kustomization, revision, events.EventSeverityInfo, changeSet, nil)
		}
	}
	return changeSet, nil
}

func (r *KustomizationReconciler) prune(client client.Client, kustomization kustomizev1.Kustomization, snapshot *kustomizev1.Snapshot) error {
	if !kustomization.Spec.Prune || kustomization.Status.Snapshot == nil || snapshot == nil {
		return nil
	}
	if kustomization.DeletionTimestamp.IsZero() && kustomization.Status.Snapshot.Checksum == snapshot.Checksum {
		return nil
	}

	gc := NewGarbageCollector(client, *kustomization.Status.Snapshot, r.Log)

	if output, ok := gc.Prune(kustomization.GetTimeout(),
		kustomization.GetName(),
		kustomization.GetNamespace(),
	); !ok {
		return fmt.Errorf("garbage collection failed: %s", output)
	} else {
		if output != "" {
			r.Log.WithValues(
				strings.ToLower(kustomization.Kind),
				fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
			).Info(fmt.Sprintf("garbage collection completed: %s", output))
			r.event(kustomization, snapshot.Checksum, events.EventSeverityInfo, output, nil)
		}
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(statusPoller *polling.StatusPoller, kustomization kustomizev1.Kustomization, revision string, changed bool) error {
	if len(kustomization.Spec.HealthChecks) == 0 {
		return nil
	}

	hc := NewHealthCheck(kustomization, statusPoller)

	if err := hc.Assess(1 * time.Second); err != nil {
		return err
	}

	if kustomization.Status.LastAppliedRevision != revision && changed {
		r.event(kustomization, revision, events.EventSeverityInfo, "Health check passed", nil)
	}
	return nil
}

func (r *KustomizationReconciler) parseApplyOutput(in []byte) map[string]string {
	result := make(map[string]string)
	input := strings.Split(string(in), "\n")
	if len(input) == 0 {
		return result
	}
	var parts []string
	for _, str := range input {
		if str != "" {
			parts = append(parts, str)
		}
	}
	for _, str := range parts {
		kv := strings.Split(str, " ")
		if len(kv) > 1 {
			result[kv[0]] = kv[1]
		}
	}
	return result
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

func (r *KustomizationReconciler) requestsForGitRepositoryRevisionChange(obj handler.MapObject) []reconcile.Request {
	repo, ok := obj.Object.(*sourcev1.GitRepository)
	if !ok {
		panic(fmt.Sprintf("Expected a GitRepository but got a %T", obj))
	}
	// If we do not have an artifact, we have no requests to make
	if repo.GetArtifact() == nil {
		return nil
	}

	ctx := context.Background()
	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list, client.MatchingFields{
		kustomizev1.SourceIndexKey: fmt.Sprintf("%s/%s", obj.Meta.GetNamespace(), obj.Meta.GetName()),
	}); err != nil {
		r.Log.Error(err, "failed to list Kustomizations for GitRepository")
		return nil
	}
	var dd []dependency.Dependent
	for _, d := range list.Items {
		// If the revision of the artifact equals to the last attempted revision,
		// we should not make a request for this Kustomization
		if repo.GetArtifact().Revision == d.Status.LastAttemptedRevision {
			continue
		}
		dd = append(dd, d)
	}
	sorted, err := dependency.Sort(dd)
	if err != nil {
		r.Log.Error(err, "unable to dependency sort Kustomization list")
		return nil
	}
	reqs := make([]reconcile.Request, len(sorted), len(sorted))
	for i := range sorted {
		reqs[i].NamespacedName.Name = sorted[i].Name
		reqs[i].NamespacedName.Namespace = sorted[i].Namespace

		r.Log.Info("requesting reconciliation due to GitRepository revision change",
			strings.ToLower(kustomizev1.KustomizationKind), &reqs[i].NamespacedName,
			"revision", repo.GetArtifact().Revision)
	}
	return reqs
}

func (r *KustomizationReconciler) requestsForBucketRevisionChange(obj handler.MapObject) []reconcile.Request {
	bucket, ok := obj.Object.(*sourcev1.Bucket)
	if !ok {
		panic(fmt.Sprintf("Expected a Bucket but got a %T", obj))
	}
	// If we do not have an artifact, we have no requests to make
	if bucket.GetArtifact() == nil {
		return nil
	}

	ctx := context.Background()
	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list, client.MatchingFields{
		kustomizev1.BucketIndexKey: fmt.Sprintf("%s/%s", obj.Meta.GetNamespace(), obj.Meta.GetName()),
	}); err != nil {
		r.Log.Error(err, "failed to list Kustomizations for Bucket")
		return nil
	}
	var dd []dependency.Dependent
	for _, d := range list.Items {
		// If the revision of the artifact equals to the last attempted revision,
		// we should not make a request for this Kustomization
		if bucket.GetArtifact().Revision == d.Status.LastAttemptedRevision {
			continue
		}
		dd = append(dd, d)
	}
	sorted, err := dependency.Sort(dd)
	if err != nil {
		r.Log.Error(err, "unable to dependency sort Kustomization list")
		return nil
	}
	reqs := make([]reconcile.Request, len(sorted), len(sorted))
	for i := range sorted {
		reqs[i].NamespacedName.Name = sorted[i].Name
		reqs[i].NamespacedName.Namespace = sorted[i].Namespace

		r.Log.Info("requesting reconciliation due to Bucket revision change",
			strings.ToLower(kustomizev1.KustomizationKind), &reqs[i].NamespacedName,
			"revision", bucket.GetArtifact().Revision)
	}
	return reqs
}

func (r *KustomizationReconciler) event(kustomization kustomizev1.Kustomization, revision, severity, msg string, metadata map[string]string) {
	r.EventRecorder.Event(&kustomization, "Normal", severity, msg)
	objRef, err := reference.GetReference(r.Scheme, &kustomization)
	if err != nil {
		r.Log.WithValues(
			strings.ToLower(kustomization.Kind),
			fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
		).Error(err, "unable to send event")
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
			r.Log.WithValues(
				strings.ToLower(kustomization.Kind),
				fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
			).Error(err, "unable to send event")
			return
		}
	}
}

func (r *KustomizationReconciler) recordReadiness(kustomization kustomizev1.Kustomization) {
	if r.MetricsRecorder == nil {
		return
	}

	objRef, err := reference.GetReference(r.Scheme, &kustomization)
	if err != nil {
		r.Log.WithValues(
			strings.ToLower(kustomization.Kind),
			fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
		).Error(err, "unable to record readiness metric")
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

func (r *KustomizationReconciler) newKustomizationClient(kustomization kustomizev1.Kustomization) (client.Client, *polling.StatusPoller, error) {
	if kustomization.Spec.KubeConfig == nil {
		// TODO: implement impersonation overrides for in-cluster
		return r.Client, r.StatusPoller, nil
	}

	kubeConfigBytes, err := r.getKubeConfig(kustomization)
	if err != nil {
		return nil, nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		return nil, nil, err
	}

	// TODO: implement impersonation overrides on the target cluster restConfig

	restMapper, err := apiutil.NewDynamicRESTMapper(restConfig)
	if err != nil {
		return nil, nil, err
	}

	// this client does not have a cache like the normal controller-runtime default client
	// this is intentional but one could be added
	client, err := client.New(restConfig, client.Options{Mapper: restMapper})
	if err != nil {
		return nil, nil, err
	}

	statusPoller := polling.NewStatusPoller(client, restMapper)

	return client, statusPoller, err
}

func (r *KustomizationReconciler) updateStatus(ctx context.Context, req ctrl.Request, newStatus kustomizev1.KustomizationStatus) error {
	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return err
	}

	patch := client.MergeFrom(kustomization.DeepCopy())
	kustomization.Status = newStatus

	return r.Status().Patch(ctx, &kustomization, patch)
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
