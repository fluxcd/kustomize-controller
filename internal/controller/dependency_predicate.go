/*
Copyright 2025 The Flux authors

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

package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/fluxcd/pkg/runtime/conditions"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

type KustomizationReadyChangePredicate struct {
	predicate.Funcs
}

func (KustomizationReadyChangePredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectNew == nil || e.ObjectOld == nil {
		return false
	}

	newKs, ok := e.ObjectNew.(*kustomizev1.Kustomization)
	if !ok {
		return false
	}
	oldKs, ok := e.ObjectOld.(*kustomizev1.Kustomization)
	if !ok {
		return false
	}

	if !conditions.IsReady(newKs) {
		return false
	}
	if !conditions.IsReady(oldKs) {
		return true
	}

	return oldKs.Status.LastAppliedRevision != newKs.Status.LastAppliedRevision
}
