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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
)

type BucketRevisionChangePredicate struct {
	predicate.Funcs
}

func (BucketRevisionChangePredicate) Update(e event.UpdateEvent) bool {
	if e.MetaOld == nil || e.MetaNew == nil {
		return false
	}

	oldRepo, ok := e.ObjectOld.(*sourcev1.Bucket)
	if !ok {
		return false
	}

	newRepo, ok := e.ObjectNew.(*sourcev1.Bucket)
	if !ok {
		return false
	}

	if oldRepo.GetArtifact() == nil && newRepo.GetArtifact() != nil {
		return true
	}

	if oldRepo.GetArtifact() != nil && newRepo.GetArtifact() != nil &&
		oldRepo.GetArtifact().Checksum != newRepo.GetArtifact().Checksum {
		return true
	}

	return false
}

func (BucketRevisionChangePredicate) Create(e event.CreateEvent) bool {
	return false
}

func (BucketRevisionChangePredicate) Delete(e event.DeleteEvent) bool {
	return false
}
