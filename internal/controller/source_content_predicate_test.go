/*
Copyright 2026 The Flux authors

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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

func TestSourceRevisionChangePredicate_Content(t *testing.T) {
	base := &sourcev1.GitRepository{Status: sourcev1.GitRepositoryStatus{
		Artifact: &meta.Artifact{Revision: "main@sha1:abc", Digest: "sha256:aaa"},
	}}
	for _, tt := range []struct {
		name   string
		change func(*sourcev1.GitRepository)
		want   bool
	}{
		{"unchanged", func(*sourcev1.GitRepository) {}, false},
		{"digest only", func(r *sourcev1.GitRepository) { r.Status.Artifact.Digest = "sha256:bbb" }, true},
		{"revision only", func(r *sourcev1.GitRepository) { r.Status.Artifact.Revision = "main@sha1:def" }, true},
		{"timestamp only", func(r *sourcev1.GitRepository) { r.Status.Artifact.LastUpdateTime = metav1.Now() }, false},
		{"url only", func(r *sourcev1.GitRepository) { r.Status.Artifact.URL = "http://localhost/new" }, false},
		{"conditions only", func(r *sourcev1.GitRepository) {
			r.Status.Conditions = []metav1.Condition{{Type: meta.ReadyCondition, Status: metav1.ConditionTrue}}
		}, false},
		{"artifact removed", func(r *sourcev1.GitRepository) { r.Status.Artifact = nil }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updated := base.DeepCopy()
			tt.change(updated)
			if got := (SourceRevisionChangePredicate{}).Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}); got != tt.want {
				t.Fatalf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
	p := SourceRevisionChangePredicate{}
	if !p.Update(event.UpdateEvent{ObjectOld: &sourcev1.GitRepository{}, ObjectNew: base}) {
		t.Fatal("first artifact must enqueue dependents")
	}
	if p.Update(event.UpdateEvent{ObjectNew: base}) || p.Update(event.UpdateEvent{ObjectOld: base}) {
		t.Fatal("incomplete events must be ignored")
	}
}
