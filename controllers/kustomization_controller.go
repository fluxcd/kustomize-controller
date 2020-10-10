/*
Copyright 2020 The Flux CD contributors.

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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/krusty"
	kustypes "sigs.k8s.io/kustomize/api/types"

	"github.com/fluxcd/kustomize-controller/internal/metrics"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/recorder"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	requeueDependency     time.Duration
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EventRecorder         kuberecorder.EventRecorder
	ExternalEventRecorder *recorder.EventRecorder
	MetricsRecorder       *metrics.Recorder
	StatusPoller          *polling.StatusPoller
}

// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *KustomizationReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	syncStart := time.Now()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues("controller", strings.ToLower(kustomizev1.KustomizationKind), "request", req.NamespacedName)

	// Examine if the object is under deletion
	if kustomization.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(kustomization.ObjectMeta.Finalizers, kustomizev1.KustomizationFinalizer) {
			kustomization.ObjectMeta.Finalizers = append(kustomization.ObjectMeta.Finalizers, kustomizev1.KustomizationFinalizer)
			if err := r.Update(ctx, &kustomization); err != nil {
				log.Error(err, "unable to register finalizer")
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(kustomization.ObjectMeta.Finalizers, kustomizev1.KustomizationFinalizer) {
			// Our finalizer is still present, so lets handle garbage collection
			if kustomization.Spec.Prune && !kustomization.Spec.Suspend {
				if err := r.prune(kustomization, kustomization.Status.Snapshot, true); err != nil {
					r.event(kustomization, kustomization.Status.LastAppliedRevision, recorder.EventSeverityError, "pruning for deleted resource failed", nil)
					// Return the error so we retry the failed garbage collection
					return ctrl.Result{}, err
				}
			}
			// Record deleted status
			r.recordReadiness(kustomization, true)

			// Remove our finalizer from the list and update it
			kustomization.ObjectMeta.Finalizers = removeString(kustomization.ObjectMeta.Finalizers, kustomizev1.KustomizationFinalizer)
			if err := r.Update(ctx, &kustomization); err != nil {
				return ctrl.Result{}, err
			}
			// Stop reconciliation as the object is being deleted
			return ctrl.Result{}, nil
		}
	}

	if kustomization.Spec.Suspend {
		msg := "Kustomization is suspended, skipping reconciliation"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", meta.SuspendedReason, msg)
		if err := r.Status().Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to update status")
			return ctrl.Result{Requeue: true}, err
		}
		r.recordReadiness(kustomization, false)
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	// set the reconciliation status to progressing
	kustomization = kustomizev1.KustomizationProgressing(kustomization)
	if err := r.Status().Update(ctx, &kustomization); err != nil {
		log.Error(err, "unable to update status")
		return ctrl.Result{Requeue: true}, err
	}
	r.recordReadiness(kustomization, false)

	// resolve source reference
	source, err := r.getSource(ctx, kustomization)
	if err != nil {
		return ctrl.Result{}, err
	}

	// check source readiness
	if source.GetArtifact() == nil {
		msg := "Source is not ready"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.ArtifactFailedReason, msg)
		if err := r.Status().Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to update status")
			return ctrl.Result{Requeue: true}, err
		}
		r.recordReadiness(kustomization, false)
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(kustomization); err != nil {
			kustomization = kustomizev1.KustomizationNotReady(
				kustomization, source.GetArtifact().Revision, meta.DependencyNotReadyReason, err.Error())
			if err := r.Status().Update(ctx, &kustomization); err != nil {
				log.Error(err, "unable to update status")
				return ctrl.Result{Requeue: true}, err
			}
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue on a fix interval.
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			log.Error(err, msg)
			r.event(kustomization, source.GetArtifact().Revision, recorder.EventSeverityInfo, msg, nil)
			r.recordReadiness(kustomization, false)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}
		log.Info("All dependencies area ready, proceeding with reconciliation")
	}

	// reconcile kustomization by applying the latest revision
	reconciledKustomization, reconcileErr := r.reconcile(*kustomization.DeepCopy(), source)
	if reconcileErr != nil {
		// broadcast the error
		r.event(kustomization, source.GetArtifact().Revision, recorder.EventSeverityError, reconcileErr.Error(), nil)
	}

	// update status
	if err := r.Status().Update(ctx, &reconciledKustomization); err != nil {
		log.Error(err, "unable to update status after reconciliation")
		return ctrl.Result{Requeue: true}, err
	}

	log.Info(fmt.Sprintf("Reconciliation finished in %s, next run in %s",
		time.Now().Sub(syncStart).String(),
		kustomization.Spec.Interval.Duration.String()),
		"revision",
		source.GetArtifact().Revision,
	)

	// requeue
	if reconcileErr != nil {
		// record the reconciliation error
		r.recordReadiness(reconciledKustomization, false)
		return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, reconcileErr
	}

	// record the reconciliation result
	r.event(reconciledKustomization, source.GetArtifact().Revision, recorder.EventSeverityInfo,
		"Update completed", map[string]string{"commit_status": "update"})
	r.recordReadiness(reconciledKustomization, false)
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

type KustomizationReconcilerOptions struct {
	MaxConcurrentReconciles   int
	DependencyRequeueInterval time.Duration
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	r.requeueDependency = opts.DependencyRequeueInterval
	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}).
		WithEventFilter(predicates.ChangePredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *KustomizationReconciler) reconcile(
	kustomization kustomizev1.Kustomization,
	source sourcev1.Source) (kustomizev1.Kustomization, error) {
	// record the value of the reconciliation request, if any
	if v, ok := kustomization.GetAnnotations()[meta.ReconcileAtAnnotation]; ok {
		kustomization.Status.LastHandledReconcileAt = v
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
	err = r.applyWithRetry(kustomization, source.GetArtifact().Revision, dirPath, 5*time.Second)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			meta.ReconciliationFailedReason,
			err.Error(),
		), err
	}

	// prune
	err = r.prune(kustomization, snapshot, false)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.PruneFailedReason,
			err.Error(),
		), err
	}

	// health assessment
	err = r.checkHealth(kustomization, source.GetArtifact().Revision)
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
			return source, fmt.Errorf("source '%s' not found: %w", namespacedName, err)
		}
		source = &repository
	case sourcev1.BucketKind:
		var bucket sourcev1.Bucket
		err := r.Client.Get(ctx, namespacedName, &bucket)
		if err != nil {
			return source, fmt.Errorf("source '%s' not found: %w", namespacedName, err)
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

func (r *KustomizationReconciler) validate(kustomization kustomizev1.Kustomization, dirPath string) error {
	if kustomization.Spec.Validation == "" {
		return nil
	}

	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --dry-run=%s --cache-dir=/tmp",
		dirPath, kustomization.GetUID(), kustomization.GetTimeout().String(), kustomization.Spec.Validation)

	if kustomization.Spec.KubeConfig != nil {
		kubeConfig, err := r.getKubeConfig(kustomization, dirPath)
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

func (r *KustomizationReconciler) getKubeConfig(kustomization kustomizev1.Kustomization, dirPath string) (string, error) {
	timeout := kustomization.GetTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	secretName := types.NamespacedName{
		Namespace: kustomization.GetNamespace(),
		Name:      kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	var secret corev1.Secret
	if err := r.Get(ctx, secretName, &secret); err != nil {
		return "", fmt.Errorf("unable to read KubeConfig secret '%s' error: %w", secretName.String(), err)
	}

	if kubeConfig, ok := secret.Data["value"]; ok {
		kubeConfigPath := path.Join(dirPath, secretName.Name)
		if err := ioutil.WriteFile(kubeConfigPath, kubeConfig, os.ModePerm); err != nil {
			return "", fmt.Errorf("unable to write KubeConfig secret '%s' to storage: %w", secretName.String(), err)
		}
	} else {
		return "", fmt.Errorf("KubeConfig secret '%s' doesn't contain a 'value' key ", secretName.String())
	}

	return secretName.Name, nil
}

func (r *KustomizationReconciler) getServiceAccountToken(kustomization kustomizev1.Kustomization) (string, error) {
	namespacedName := types.NamespacedName{
		Namespace: kustomization.Spec.ServiceAccount.Namespace,
		Name:      kustomization.Spec.ServiceAccount.Name,
	}

	var serviceAccount corev1.ServiceAccount
	err := r.Client.Get(context.TODO(), namespacedName, &serviceAccount)
	if err != nil {
		return "", err
	}

	secretName := types.NamespacedName{
		Namespace: kustomization.Spec.ServiceAccount.Namespace,
		Name:      kustomization.Spec.ServiceAccount.Name,
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
		kubeConfig, err := r.getKubeConfig(kustomization, dirPath)
		if err != nil {
			return "", err
		}
		cmd = fmt.Sprintf("%s --kubeconfig=%s", cmd, kubeConfig)
	} else {
		// impersonate SA
		if kustomization.Spec.ServiceAccount != nil {
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

func (r *KustomizationReconciler) applyWithRetry(kustomization kustomizev1.Kustomization, revision, dirPath string, delay time.Duration) error {
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
				return err
			} else {
				if changeSet != "" {
					r.event(kustomization, revision, recorder.EventSeverityInfo, changeSet, nil)
				}
			}
		} else {
			return err
		}
	} else {
		if changeSet != "" && kustomization.Status.LastAppliedRevision != revision {
			r.event(kustomization, revision, recorder.EventSeverityInfo, changeSet, nil)
		}
	}
	return nil
}

func (r *KustomizationReconciler) prune(kustomization kustomizev1.Kustomization, snapshot *kustomizev1.Snapshot, force bool) error {
	if kustomization.Spec.KubeConfig != nil {
		// TODO: implement pruning for remote clusters
		r.Log.WithValues(
			strings.ToLower(kustomization.Kind),
			fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
		).V(2).Info("skipping pruning, garbage collection is not implemented for remote clusters")
		return nil
	}
	if kustomization.Status.Snapshot == nil || snapshot == nil {
		return nil
	}
	if !force {
		if kustomization.Status.Snapshot.Checksum == snapshot.Checksum {
			return nil
		}
	}

	gc := NewGarbageCollector(r.Client, *kustomization.Status.Snapshot, r.Log)

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
			r.event(kustomization, snapshot.Checksum, recorder.EventSeverityInfo, output, nil)
		}
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(kustomization kustomizev1.Kustomization, revision string) error {
	if len(kustomization.Spec.HealthChecks) == 0 {
		return nil
	}

	hc := NewHealthCheck(kustomization, r.StatusPoller)

	if err := hc.Assess(1 * time.Second); err != nil {
		return err
	}

	if kustomization.Status.LastAppliedRevision != revision {
		r.event(kustomization, revision, recorder.EventSeverityInfo, "Health check passed", nil)
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

		if c := meta.GetCondition(k.Status.Conditions, meta.ReadyCondition); c == nil || c.Status != corev1.ConditionTrue {
			return fmt.Errorf("dependency '%s' is not ready", dName)
		}
	}

	return nil
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
		if c := meta.GetCondition(kustomization.Status.Conditions, meta.ReadyCondition); c != nil {
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

func (r *KustomizationReconciler) recordReadiness(kustomization kustomizev1.Kustomization, deleted bool) {
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
	if rc := meta.GetCondition(kustomization.Status.Conditions, meta.ReadyCondition); rc != nil {
		r.MetricsRecorder.RecordCondition(*objRef, *rc, deleted)
	} else {
		r.MetricsRecorder.RecordCondition(*objRef, meta.Condition{
			Type:   meta.ReadyCondition,
			Status: corev1.ConditionUnknown,
		}, deleted)
	}
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
