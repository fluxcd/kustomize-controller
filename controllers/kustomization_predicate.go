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
	"crypto/sha1"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

type KustomizationSyncAtPredicate struct {
	predicate.Funcs
}

func (KustomizationSyncAtPredicate) Update(e event.UpdateEvent) bool {
	if e.MetaOld == nil || e.MetaNew == nil {
		// ignore objects without metadata
		return false
	}
	if e.MetaNew.GetGeneration() != e.MetaOld.GetGeneration() {
		// reconcile on spec changes
		return true
	}

	// handle syncAt annotation
	if val, ok := e.MetaNew.GetAnnotations()[kustomizev1.SyncAtAnnotation]; ok {
		if valOld, okOld := e.MetaOld.GetAnnotations()[kustomizev1.SyncAtAnnotation]; okOld {
			if val != valOld {
				return true
			}
		} else {
			return true
		}
	}

	return false
}

type KustomizationGarbageCollectPredicate struct {
	predicate.Funcs
	Log logr.Logger
}

// Delete removes all Kubernetes objects based on the prune label selector.
func (gc KustomizationGarbageCollectPredicate) Delete(e event.DeleteEvent) bool {
	if k, ok := e.Object.(*kustomizev1.Kustomization); ok {
		if k.Spec.Prune != "" && !k.Spec.Suspend && k.Status.Snapshot != nil {
			gc.Log.Info("Garbage collection started",
				"kustomization", fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName()))

			prune(k.GetTimeout(), k.GetName(), k.GetNamespace(), k.Status.Snapshot, gc.Log)
		}
	}

	return true
}

func prune(timeout time.Duration, name string, namespace string, snapshot *kustomizev1.Snapshot, log logr.Logger) bool {
	selector := gcSelectors(name, namespace, snapshot.Revision)
	ok := true
	outInfo := ""
	outErr := ""
	for ns, kinds := range snapshot.NamespacedKinds() {
		for _, kind := range kinds {
			if output, err := deleteByKind(timeout, kind, ns, selector); err != nil {
				outErr += " " + err.Error()
				ok = false
			} else {
				outInfo += " " + output
			}
		}
	}
	if outErr == "" {
		log.Info("Garbage collection for namespaced objects completed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name),
			"output", outInfo)
	} else {
		log.Error(fmt.Errorf(outErr), "Garbage collection for namespaced objects failed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name))
	}

	outInfo = ""
	outErr = ""
	for _, kind := range snapshot.NonNamespacedKinds() {
		if output, err := deleteByKind(timeout, kind, "", selector); err != nil {
			outErr += " " + err.Error()
			ok = false
		} else {
			outInfo += " " + output
		}
	}
	if outErr == "" {
		log.Info("Garbage collection for non-namespaced objects completed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name),
			"output", outInfo)
	} else {
		log.Error(fmt.Errorf(outErr), "Garbage collection for non-namespaced objects failed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name))
	}

	return ok
}

func deleteByKind(timeout time.Duration, kind, namespace, selector string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()

	cmd := fmt.Sprintf("kubectl delete %s -l %s", kind, selector)
	if namespace != "" {
		cmd = fmt.Sprintf("%s -n=%s", cmd, namespace)
	}

	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s", string(output))
	} else {
		return strings.TrimSuffix(string(output), "\n"), nil
	}
}

func gcLabels(name, namespace, revision string) map[string]string {
	return map[string]string{
		"kustomization/name":     fmt.Sprintf("%s-%s", name, namespace),
		"kustomization/revision": checksum(revision),
	}
}

func gcSelectors(name, namespace, revision string) string {
	return fmt.Sprintf("kustomization/name=%s-%s,kustomization/revision=%s", name, namespace, checksum(revision))
}

func checksum(in string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(in)))
}
