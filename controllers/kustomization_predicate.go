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
	"os/exec"
	"strings"

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
		if k.Spec.Prune != "" && !k.Spec.Suspend {
			timeout := k.GetTimeout()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			resources := `$(kubectl api-resources --verbs=delete -o name | tr "\n" "," | sed -e 's/,$//')`
			cmd := fmt.Sprintf("kubectl delete %s --all-namespaces --timeout=%s -l %s",
				resources, timeout.String(), k.Spec.Prune)
			command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
			if output, err := command.CombinedOutput(); err != nil {
				gc.Log.Error(err, "Garbage collection failed",
					"output", string(output),
					strings.ToLower(k.Kind), fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName()))
			} else {
				gc.Log.Info("Garbage collection completed",
					"output", string(output),
					strings.ToLower(k.Kind), fmt.Sprintf("%s/%s", k.GetNamespace(), k.GetName()))
			}
		}
	}

	return true
}
