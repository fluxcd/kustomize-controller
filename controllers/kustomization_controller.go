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
	"sigs.k8s.io/yaml"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kuberecorder "k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	kustypes "sigs.k8s.io/kustomize/api/types"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	"github.com/fluxcd/pkg/lockedfile"
	"github.com/fluxcd/pkg/recorder"
	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
)

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	requeueDependency     time.Duration
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EventRecorder         kuberecorder.EventRecorder
	ExternalEventRecorder *recorder.EventRecorder
}

// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch

func (r *KustomizationReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	syncStart := time.Now()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues("controller", strings.ToLower(kustomizev1.KustomizationKind), "request", req.NamespacedName)

	if kustomization.Spec.Suspend {
		msg := "Kustomization is suspended, skipping reconciliation"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, "", kustomizev1.SuspendedReason, msg)
		if err := r.Status().Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to update status")
			return ctrl.Result{Requeue: true}, err
		}
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	kustomization = kustomizev1.KustomizationProgressing(kustomization)
	if err := r.Status().Update(ctx, &kustomization); err != nil {
		log.Error(err, "unable to update status")
		return ctrl.Result{Requeue: true}, err
	}

	var source sourcev1.Source

	// get artifact source from Git repository
	if kustomization.Spec.SourceRef.Kind == sourcev1.GitRepositoryKind {
		var repository sourcev1.GitRepository
		repositoryName := types.NamespacedName{
			Namespace: kustomization.GetNamespace(),
			Name:      kustomization.Spec.SourceRef.Name,
		}
		err := r.Client.Get(ctx, repositoryName, &repository)
		if err != nil {
			log.Error(err, fmt.Sprintf("GitRepository '%s' not found", repositoryName))
			return ctrl.Result{Requeue: true}, err
		}
		source = &repository
	}

	if source == nil {
		err := fmt.Errorf("source `%s` kind '%s' not supported",
			kustomization.Spec.SourceRef.Name, kustomization.Spec.SourceRef.Kind)
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
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(kustomization); err != nil {
			kustomization = kustomizev1.KustomizationNotReady(
				kustomization, source.GetArtifact().Revision, kustomizev1.DependencyNotReadyReason, err.Error())
			if err := r.Status().Update(ctx, &kustomization); err != nil {
				log.Error(err, "unable to update status")
				return ctrl.Result{Requeue: true}, err
			}
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue on a fix interval.
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", r.requeueDependency.String())
			log.Error(err, msg)
			r.event(kustomization, source.GetArtifact().Revision, recorder.EventSeverityInfo, msg)
			return ctrl.Result{RequeueAfter: r.requeueDependency}, nil
		}
		log.Info("All dependencies area ready, proceeding with reconciliation")
	}

	// reconcile kustomization by applying the latest revision
	reconciledKustomization, reconcileErr := r.reconcile(*kustomization.DeepCopy(), source)
	if reconcileErr != nil {
		// broadcast the error
		r.event(kustomization, source.GetArtifact().Revision, recorder.EventSeverityError, reconcileErr.Error())
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
		return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, reconcileErr
	}
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
		WithEventFilter(KustomizationGarbageCollectPredicate{Log: r.Log}).
		WithEventFilter(KustomizationSyncAtPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *KustomizationReconciler) reconcile(
	kustomization kustomizev1.Kustomization,
	source sourcev1.Source) (kustomizev1.Kustomization, error) {
	// acquire lock
	unlock, err := r.lock(fmt.Sprintf("%s-%s", kustomization.GetName(), kustomization.GetNamespace()))
	if err != nil {
		err = fmt.Errorf("lockfile error: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			sourcev1.StorageOperationFailedReason,
			err.Error(),
		), err
	}
	defer unlock()

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
			"artifact acquisition failed",
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

	// generate kustomization.yaml
	err = r.generate(kustomization, source.GetArtifact().Revision, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			"kustomize create failed",
		), err
	}

	// kustomize build
	snapshot, err := r.build(kustomization, source.GetArtifact().Revision, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.BuildFailedReason,
			"kustomize build failed",
		), err
	}

	// dry-run apply
	err = r.validate(kustomization, dirPath)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ValidationFailedReason,
			fmt.Sprintf("%s-side validation failed", kustomization.Spec.Validation),
		), err
	}

	// apply
	err = r.applyWithRetry(kustomization, source.GetArtifact().Revision, dirPath, 5*time.Second)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			source.GetArtifact().Revision,
			kustomizev1.ApplyFailedReason,
			"apply failed",
		), err
	}

	// prune
	err = r.prune(kustomization, snapshot)
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
			"health check failed",
		), err
	}

	return kustomizev1.KustomizationReady(
		kustomization,
		snapshot,
		source.GetArtifact().Revision,
		kustomizev1.ApplySucceedReason,
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

func (r *KustomizationReconciler) generate(kustomization kustomizev1.Kustomization, revision, dirPath string) error {
	kfile := filepath.Join(dirPath, kustomizationFileName)

	if _, err := os.Stat(kfile); err != nil {
		timeout := kustomization.GetTimeout() + (time.Second * 1)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := fmt.Sprintf("cd %s && kustomize create --autodetect --recursive", dirPath)
		command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
		output, err := command.CombinedOutput()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("kustomize create failed: %s", string(output))
		}
	}

	if err := r.generateLabelTransformer(kustomization, revision, dirPath); err != nil {
		return err
	}

	data, err := ioutil.ReadFile(kfile)
	if err != nil {
		return err
	}

	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	if err := yaml.Unmarshal(data, &kus); err != nil {
		return err
	}

	if len(kus.Transformers) == 0 {
		kus.Transformers = []string{transformerFileName}
	} else {
		var exists bool
		for _, transformer := range kus.Transformers {
			if transformer == transformerFileName {
				exists = true
				break
			}
		}
		if !exists {
			kus.Transformers = append(kus.Transformers, transformerFileName)
		}
	}

	kd, err := yaml.Marshal(kus)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func (r *KustomizationReconciler) generateLabelTransformer(kustomization kustomizev1.Kustomization, revision, dirPath string) error {
	var lt = struct {
		ApiVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
		Metadata   struct {
			Name string `json:"name" yaml:"name"`
		} `json:"metadata" yaml:"metadata"`
		Labels     map[string]string    `json:"labels,omitempty" yaml:"labels,omitempty"`
		FieldSpecs []kustypes.FieldSpec `json:"fieldSpecs,omitempty" yaml:"fieldSpecs,omitempty"`
	}{
		ApiVersion: "builtin",
		Kind:       "LabelTransformer",
		Metadata: struct {
			Name string `json:"name" yaml:"name"`
		}{
			Name: kustomization.GetName(),
		},
		Labels: gcLabels(kustomization.GetName(), kustomization.GetNamespace(), revision),
		FieldSpecs: []kustypes.FieldSpec{
			{Path: "metadata/labels", CreateIfNotPresent: true},
		},
	}

	data, err := yaml.Marshal(lt)
	if err != nil {
		return err
	}

	labelsFile := filepath.Join(dirPath, transformerFileName)
	if err := ioutil.WriteFile(labelsFile, data, os.ModePerm); err != nil {
		return err
	}

	return nil
}

func (r *KustomizationReconciler) build(kustomization kustomizev1.Kustomization, revision, dirPath string) (*kustomizev1.Snapshot, error) {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kustomize build . > %s.yaml",
		dirPath, kustomization.GetUID())
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("kustomize build failed: %s", string(output))
	}

	manifestsFile := filepath.Join(dirPath, fmt.Sprintf("%s.yaml", kustomization.GetUID()))
	data, err := ioutil.ReadFile(manifestsFile)
	if err != nil {
		return nil, err
	}

	return kustomizev1.NewSnapshot(data, revision)
}

func (r *KustomizationReconciler) validate(kustomization kustomizev1.Kustomization, dirPath string) error {
	if kustomization.Spec.Validation == "" {
		return nil
	}

	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --dry-run=%s",
		dirPath, kustomization.GetUID(), kustomization.GetTimeout().String(), kustomization.Spec.Validation)
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

func (r *KustomizationReconciler) apply(kustomization kustomizev1.Kustomization, revision, dirPath string) (string, error) {
	start := time.Now()
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s",
		dirPath, kustomization.GetUID(), kustomization.Spec.Interval.Duration.String())

	// impersonate SA
	if sa := kustomization.Spec.ServiceAccount; sa != nil {
		cmd = fmt.Sprintf("%s --as system:serviceaccount:%s:%s", cmd, sa.Namespace, sa.Name)
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
	changeSet, err := r.apply(kustomization, revision, dirPath)
	if err != nil {
		// retry apply due to CRD/CR race
		if strings.Contains(err.Error(), "could not find the requested resource") ||
			strings.Contains(err.Error(), "no matches for kind") {
			r.Log.Info("retrying apply",
				"error", err.Error(),
				"kustomization", fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()))
			time.Sleep(delay)
			if changeSet, err := r.apply(kustomization, revision, dirPath); err != nil {
				return err
			} else {
				if changeSet != "" {
					r.event(kustomization, revision, recorder.EventSeverityInfo, changeSet)
				}
			}
		} else {
			return err
		}
	} else {
		if changeSet != "" && kustomization.Status.LastAppliedRevision != revision {
			r.event(kustomization, revision, recorder.EventSeverityInfo, changeSet)
		}
	}
	return nil
}

func (r *KustomizationReconciler) prune(kustomization kustomizev1.Kustomization, snapshot *kustomizev1.Snapshot) error {
	if kustomization.Status.Snapshot == nil || snapshot == nil {
		return nil
	}
	if kustomization.Status.Snapshot.Revision == snapshot.Revision {
		return nil
	}

	if output, ok := prune(kustomization.GetTimeout(),
		kustomization.GetName(),
		kustomization.GetNamespace(),
		kustomization.Status.Snapshot,
		r.Log,
	); !ok {
		return fmt.Errorf("pruning failed")
	} else {
		changeSet := ""
		input := strings.Split(output, "\n")
		for _, action := range input {
			if strings.Contains(action, "deleted") {
				changeSet += action + "\n"
			}
		}

		if changeSet != "" {
			r.event(kustomization, snapshot.Revision, recorder.EventSeverityInfo, changeSet)
		}
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(kustomization kustomizev1.Kustomization, revision string) error {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var alerts string

	for _, check := range kustomization.Spec.HealthChecks {
		cmd := fmt.Sprintf("until kubectl -n %s get %s %s ; do sleep 2; done",
			check.Namespace, check.Kind, check.Name)
		command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
		if _, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("health check timeout for %s '%s/%s': %w",
				check.Kind, check.Namespace, check.Name, err)
		}

		cmd = fmt.Sprintf("kubectl -n %s rollout status %s %s --timeout=%s",
			check.Namespace, check.Kind, check.Name, kustomization.GetTimeout())
		command = exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
		output, err := command.CombinedOutput()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("health check timeout for %s '%s/%s': %w",
					check.Kind, check.Namespace, check.Name, err)
			}
			return fmt.Errorf("health check failed for %s '%s/%s': %s",
				check.Kind, check.Namespace, check.Name, string(output))
		} else {
			msg := fmt.Sprintf("Health check passed for %s '%s/%s'",
				check.Kind, check.Namespace, check.Name)
			r.Log.WithValues(
				strings.ToLower(kustomization.Kind),
				fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
			).Info(msg)
			alerts += msg + "\n"
		}
	}

	if alerts != "" && kustomization.Status.LastAppliedRevision != revision {
		r.event(kustomization, revision, recorder.EventSeverityInfo, alerts)
	}
	return nil
}

func (r *KustomizationReconciler) lock(name string) (unlock func(), err error) {
	lockFile := path.Join(os.TempDir(), name+".lock")
	mutex := lockedfile.MutexAt(lockFile)
	return mutex.Lock()
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
	for _, dep := range kustomization.Spec.DependsOn {
		depName := types.NamespacedName{
			Namespace: kustomization.GetNamespace(),
			Name:      dep,
		}
		var k kustomizev1.Kustomization
		err := r.Get(context.Background(), depName, &k)
		if err != nil {
			return fmt.Errorf("unable to get '%s' dependency: %w", depName, err)
		}

		if len(k.Status.Conditions) == 0 {
			return fmt.Errorf("dependency '%s' is not ready", depName)
		}

		for _, condition := range k.Status.Conditions {
			if condition.Type == kustomizev1.ReadyCondition && condition.Status != corev1.ConditionTrue {
				return fmt.Errorf("dependency '%s' is not ready", depName)
			}
		}
	}

	return nil
}

func (r *KustomizationReconciler) event(kustomization kustomizev1.Kustomization, revision, severity, msg string) {
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
		var meta map[string]string
		if revision != "" {
			meta = map[string]string{"revision": revision}
		}
		if err := r.ExternalEventRecorder.Eventf(*objRef, meta, severity, severity, msg); err != nil {
			r.Log.WithValues(
				strings.ToLower(kustomization.Kind),
				fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
			).Error(err, "unable to send event")
			return
		}
	}
}

var (
	kustomizationFileName = "kustomization.yaml"
	transformerFileName   = "kustomization-gc-labels.yaml"
)
