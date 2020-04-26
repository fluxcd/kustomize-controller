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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	"github.com/fluxcd/kustomize-controller/internal/alert"
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
	ctx := context.Background()
	syncStart := time.Now()

	var kustomization kustomizev1.Kustomization
	if err := r.Get(ctx, req.NamespacedName, &kustomization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues(strings.ToLower(kustomization.Kind), req.NamespacedName)

	kustomization = kustomizev1.KustomizationProgressing(kustomization)
	if err := r.Status().Update(ctx, &kustomization); err != nil {
		log.Error(err, "unable to update Kustomization status")
		return ctrl.Result{Requeue: true}, err
	}

	if kustomization.Spec.Suspend {
		msg := "Kustomization is suspended, skipping execution"
		kustomization = kustomizev1.KustomizationNotReady(kustomization, kustomizev1.SuspendedReason, msg)
		if err := r.Status().Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to update Kustomization status")
			return ctrl.Result{Requeue: true}, err
		}
		log.Info(msg)
		return ctrl.Result{}, nil
	}

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

	// check source readiness
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

	// check dependencies
	if len(kustomization.Spec.DependsOn) > 0 {
		if err := r.checkDependencies(kustomization); err != nil {
			kustomization = kustomizev1.KustomizationNotReady(kustomization, kustomizev1.DependencyNotReadyReason, err.Error())
			if err := r.Status().Update(ctx, &kustomization); err != nil {
				log.Error(err, "unable to update Kustomization status")
				return ctrl.Result{Requeue: true}, err
			}
			// we can't rely on exponential backoff because it will prolong the execution too much,
			// instead we requeue every half a minute.
			requeueAfter := 30 * time.Second
			msg := fmt.Sprintf("Dependencies do not meet ready condition, retrying in %s", requeueAfter.String())
			log.Error(err, msg)
			r.alert(kustomization, msg, "info")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		log.Info("All dependencies area ready, proceeding with apply")
	}

	// try sync
	syncedKustomization, err := r.sync(*kustomization.DeepCopy(), source)
	if err != nil {
		log.Error(err, "Kustomization apply failed")
		r.alert(kustomization, err.Error(), "error")
	}

	// update status
	if err := r.Status().Update(ctx, &syncedKustomization); err != nil {
		log.Error(err, "unable to update Kustomization status")
		return ctrl.Result{Requeue: true}, err
	}

	// log sync duration
	log.Info(fmt.Sprintf("Kustomization sync finished in %s, next run in %s",
		time.Now().Sub(syncStart).String(),
		kustomization.Spec.Interval.Duration.String(),
	))

	// requeue kustomization
	return ctrl.Result{RequeueAfter: kustomization.Spec.Interval.Duration}, nil
}

func (r *KustomizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}).
		WithEventFilter(KustomizationGarbageCollectPredicate{Log: r.Log}).
		WithEventFilter(KustomizationSyncAtPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}

func (r *KustomizationReconciler) sync(
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
	err = r.download(kustomization, source.GetArtifact().URL, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ArtifactFailedReason,
			"artifact acquisition failed",
		), err
	}

	// check build path exists
	if _, err := os.Stat(path.Join(tmpDir, kustomization.Spec.Path)); err != nil {
		err = fmt.Errorf("kustomization path not found: %w", err)
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ArtifactFailedReason,
			err.Error(),
		), err
	}

	// generate kustomization.yaml
	err = r.generate(kustomization, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.BuildFailedReason,
			"kustomize create failed",
		), err
	}

	// kustomize build
	err = r.build(kustomization, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.BuildFailedReason,
			"kustomize build failed",
		), err
	}

	// dry-run apply
	err = r.validate(kustomization, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ValidationFailedReason,
			fmt.Sprintf("%s-side validation failed", kustomization.Spec.Validation),
		), err
	}

	// apply
	err = r.apply(kustomization, tmpDir)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.ApplyFailedReason,
			"apply failed",
		), err
	}

	// health assessment
	err = r.checkHealth(kustomization)
	if err != nil {
		return kustomizev1.KustomizationNotReady(
			kustomization,
			kustomizev1.HealthCheckFailedReason,
			"health check failed",
		), err
	}

	return kustomizev1.KustomizationReady(
		kustomization,
		source.GetArtifact().Revision,
		kustomizev1.ApplySucceedReason,
		"kustomization was successfully applied",
	), nil
}

func (r *KustomizationReconciler) download(kustomization kustomizev1.Kustomization, url string, tmpDir string) error {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && curl -sL %s | tar -xz --strip-components=1 -C .",
		tmpDir, url)
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("artifact `%s` download failed: %s", url, string(output))
	}
	return nil
}

func (r *KustomizationReconciler) generate(kustomization kustomizev1.Kustomization, tmpDir string) error {
	if !kustomization.Spec.Generate {
		return nil
	}

	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dirPath := path.Join(tmpDir, kustomization.Spec.Path)
	cmd := fmt.Sprintf("cd %s && kustomize create --autodetect --recursive", dirPath)
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("kustomize create failed: %s", string(output))
	}

	if err := r.generateLabelTransformer(kustomization, dirPath); err != nil {
		return err
	}

	return nil
}

func (r *KustomizationReconciler) generateLabelTransformer(kustomization kustomizev1.Kustomization, dirPath string) error {
	labelTransformer := `
apiVersion: builtin
kind: LabelTransformer
metadata:
  name: labels
labels:
{{- range $key, $value := . }}
  {{ $key }}: {{ $value }}
{{- end }}
fieldSpecs:
  - path: metadata/labels
    create: true
`

	transformers := `
transformers:
  - gc-labels.yaml
`

	prune := kustomization.Spec.Prune
	if prune == "" {
		return nil
	}

	// transform prune into label selectors
	selectors := make(map[string]string)
	for _, ls := range strings.Split(prune, ",") {
		if kv := strings.Split(ls, "="); len(kv) == 2 {
			selectors[kv[0]] = kv[1]
		}
	}

	t, err := template.New("tmpl").Parse(labelTransformer)
	if err != nil {
		return fmt.Errorf("labelTransformer template parsing failed: %w", err)
	}

	var data bytes.Buffer
	writer := bufio.NewWriter(&data)
	if err := t.Execute(writer, selectors); err != nil {
		return fmt.Errorf("labelTransformer template execution failed: %w", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("labelTransformer flush failed: %w", err)
	}

	file, err := os.Create(path.Join(dirPath, "gc-labels.yaml"))
	if err != nil {
		return fmt.Errorf("labelTransformer create failed: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(data.String()); err != nil {
		return fmt.Errorf("labelTransformer write failed: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("labelTransformer sync failed: %w", err)
	}

	kfile, err := os.OpenFile(path.Join(dirPath, "kustomization.yaml"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("kustomization file open failed: %w", err)
	}
	defer kfile.Close()

	if _, err := kfile.WriteString(transformers); err != nil {
		return fmt.Errorf("kustomization file append failed: %w", err)
	}

	return nil
}

func (r *KustomizationReconciler) build(kustomization kustomizev1.Kustomization, tmpDir string) error {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kustomize build %s > %s.yaml",
		tmpDir, kustomization.Spec.Path, kustomization.GetName())
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("kustomize build failed: %s", string(output))
	}
	return nil
}

func (r *KustomizationReconciler) validate(kustomization kustomizev1.Kustomization, tmpDir string) error {
	if kustomization.Spec.Validation == "" {
		return nil
	}

	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s --dry-run=%s",
		tmpDir, kustomization.GetName(), kustomization.GetTimeout().String(), kustomization.Spec.Validation)
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

func (r *KustomizationReconciler) apply(kustomization kustomizev1.Kustomization, tmpDir string) error {
	start := time.Now()
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := fmt.Sprintf("cd %s && kubectl apply -f %s.yaml --timeout=%s",
		tmpDir, kustomization.GetName(), kustomization.Spec.Interval.Duration.String())
	if kustomization.Spec.Prune != "" {
		cmd = fmt.Sprintf("%s --prune -l %s", cmd, kustomization.Spec.Prune)
	}
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("apply timeout: %w", err)
		}
		return fmt.Errorf("apply failed: %s", string(output))
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

	var diff bool
	for _, action := range resources {
		if action != "unchanged" {
			diff = true
			break
		}
	}
	if diff {
		r.alert(kustomization, string(output), "info")
	}
	return nil
}

func (r *KustomizationReconciler) checkHealth(kustomization kustomizev1.Kustomization) error {
	timeout := kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var alerts string

	for _, check := range kustomization.Spec.HealthChecks {
		cmd := fmt.Sprintf("kubectl -n %s rollout status %s %s --timeout=%s",
			check.Namespace, check.Kind, check.Name, kustomization.GetTimeout())
		command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
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

	if alerts != "" {
		r.alert(kustomization, alerts, "info")
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

func (r *KustomizationReconciler) getProfiles(kustomization kustomizev1.Kustomization) ([]kustomizev1.Profile, error) {
	list := make([]kustomizev1.Profile, 0)
	var profiles kustomizev1.ProfileList
	err := r.List(context.TODO(), &profiles, client.InNamespace(kustomization.GetNamespace()))
	if err != nil {
		return list, err
	}

	// filter profiles that match this kustomization taking into account '*' wildcard
	for _, profile := range profiles.Items {
		for _, name := range profile.Spec.Kustomizations {
			if name == kustomization.GetName() || name == "*" {
				list = append(list, profile)
				break
			}
		}
	}

	return list, nil
}

func (r *KustomizationReconciler) alert(kustomization kustomizev1.Kustomization, msg string, verbosity string) {
	profiles, err := r.getProfiles(kustomization)
	if err != nil {
		r.Log.WithValues(
			strings.ToLower(kustomization.Kind),
			fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
		).Error(err, "unable to list profiles")
		return
	}

	for _, profile := range profiles {
		if settings := profile.Spec.Alert; settings != nil {
			provider, err := alert.NewProvider(settings.Type, settings.Address, settings.Username, settings.Channel)
			if err != nil {
				r.Log.WithValues(
					strings.ToLower(kustomization.Kind),
					fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
				).Error(err, "unable to configure alert provider")
				continue
			}
			if settings.Verbosity == verbosity || verbosity == "error" {
				err = provider.Post(kustomization.GetName(), kustomization.GetNamespace(), msg, verbosity)
				if err != nil {
					r.Log.WithValues(
						strings.ToLower(kustomization.Kind),
						fmt.Sprintf("%s/%s", kustomization.GetNamespace(), kustomization.GetName()),
					).Error(err, "unable to send alert")
				}
			}
		}
	}
}
