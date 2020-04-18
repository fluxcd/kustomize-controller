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
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	"github.com/fluxcd/kustomize-controller/internal/lockedfile"
	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
)

// KustomizationReconciler reconciles a Kustomization object
type KustomizationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=kustomizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=kustomizations/status,verbs=get;update;patch

func (r *KustomizationReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues(strings.ToLower(kustomization.Kind), req.NamespacedName)

	var source sourcev1.Source

	// get artifact source from Git repository
	if kustomization.Spec.SourceRef.Kind == "GitRepository" {
		var repository sourcev1.GitRepository
		repositoryName := types.NamespacedName{
			Namespace: kustomization.GetNamespace(),
			Name:      kustomization.Spec.SourceRef.Name,
		}
		err := r.Client.Get(ctx, repositoryName, &repository)
		if err != nil {
			log.Error(err, "GitRepository not found", "gitrepository", repositoryName)
			return ctrl.Result{Requeue: true}, err
		}
		source = &repository
	}

	if source == nil {
		err := fmt.Errorf("source `%s` kind '%s' not supported",
			kustomization.Spec.SourceRef.Name, kustomization.Spec.SourceRef.Kind)
		return ctrl.Result{}, err
	}

	if source.GetArtifact() == nil {
		msg := "Source is not ready"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, kustomizev1.ArtifactFailedReason, msg)
		if err := r.Status().Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to update Kustomization status")
			return ctrl.Result{Requeue: true}, err
		}
		log.Info(msg)
		return ctrl.Result{}, nil
	}

	// try git sync
	syncedKustomization, err := r.sync(ctx, *kustomization.DeepCopy(), source)
	if err != nil {
		log.Error(err, "Kustomization apply failed")
	}

	// update status
	if err := r.Status().Update(ctx, &syncedKustomization); err != nil {
		log.Error(err, "unable to update Kustomization status")
		return ctrl.Result{Requeue: true}, err
	}

	log.Info("Kustomization sync finished, next run in " + kustomization.Spec.Interval.Duration.String())

	// requeue kustomization
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}).
		WithEventFilter(KustomizationGarbageCollectPredicate{Log: r.Log}).
		WithEventFilter(KustomizationSyncAtPredicate{}).
		Complete(r)
}

func (r *KustomizationReconciler) sync(
	ctx context.Context,
	kustomization kustomizev1.Kustomization,
	source sourcev1.Source) (kustomizev1.Kustomization, error) {
	// acquire lock
	unlock, err := r.lock(fmt.Sprintf("%s-%s", kustomization.GetName(), kustomization.GetNamespace()))
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		return kustomizev1.KustomizationNotReady(kustomization, sourcev1.StorageOperationFailedReason, err.Error()), err
	}
	defer unlock()

	// create tmp dir
	tmpDir, err := ioutil.TempDir("", kustomization.Name)
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		return kustomizev1.KustomizationNotReady(kustomization, sourcev1.StorageOperationFailedReason, err.Error()), err
	}
	defer os.RemoveAll(tmpDir)

	// download artifact and extract files
	url := source.GetArtifact().URL
	cmd := fmt.Sprintf("cd %s && curl -sL %s | tar -xz --strip-components=1 -C .", tmpDir, url)
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("artifact acquisition failed: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), fmt.Errorf("artifact download `%s` error: %s", url, string(output))
	}

	// check build path exists
	buildDir := kustomization.Spec.Path
	if _, err := os.Stat(path.Join(tmpDir, buildDir)); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// kustomize build
	cmd = fmt.Sprintf("cd %s && kustomize build %s > %s.yaml", tmpDir, buildDir, kustomization.GetName())
	command = exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err = command.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("kustomize build error: %w", err)
		fmt.Println(string(output))
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.BuildFailedReason,
			err.Error(),
		), fmt.Errorf("kustomize build error: %s", string(output))
	}

	// set apply timeout
	timeout := kustomization.Spec.Interval.Duration + (time.Second * 1)
	ctxApply, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	applyStart := time.Now()

	// run apply with timeout
	cmd = fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s",
		tmpDir, kustomization.GetName(), kustomization.Spec.Interval.Duration.String())
	if kustomization.Spec.Prune != "" {
		cmd = fmt.Sprintf("%s --prune -l %s", cmd, kustomization.Spec.Prune)
	}
	command = exec.CommandContext(ctxApply, "/bin/sh", "-c", cmd)
	output, err = command.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("kubectl apply error: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ApplyFailedReason,
			err.Error(),
		), fmt.Errorf("kubectl apply: %s", string(output))
	}

	// log apply output
	applyDuration := fmt.Sprintf("Kustomization applied in %s", time.Now().Sub(applyStart).String())
	r.Log.WithValues(
		strings.ToLower(kustomization.Kind),
		fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
	).Info(applyDuration, "output", r.parseApplyOutput(output))

	return kustomizev1.KustomizationReady(
		kustomization,
		kustomizev1.ApplySucceedReason,
		"kustomization was successfully applied",
	), nil
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
