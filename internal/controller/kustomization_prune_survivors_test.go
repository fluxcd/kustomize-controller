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

	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func newCM(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	u.SetName(name)
	u.SetNamespace("default")
	return u
}

func entry(u *unstructured.Unstructured, action ssa.Action) ssa.ChangeSetEntry {
	return ssa.ChangeSetEntry{
		ObjMetadata:  object.UnstructuredToObjMetadata(u),
		GroupVersion: u.GroupVersionKind().Version,
		Action:       action,
	}
}

// Regression test for #1664. When prune partially fails — e.g. an admission
// webhook denies some deletes — the surviving objects must be returned so the
// reconciler can re-merge them into status.Inventory and retry on the next
// reconcile. Without this, the orphans become permanently untracked.
func TestPruneSurvivors(t *testing.T) {
	g := NewWithT(t)

	a, b, c := newCM("a"), newCM("b"), newCM("c")
	objects := []*unstructured.Unstructured{a, b, c}

	t.Run("nil changeset preserves all objects on failure path", func(t *testing.T) {
		out := pruneSurvivors(objects, nil)
		g.Expect(out).To(HaveLen(3))
	})

	t.Run("deleted and skipped actions are settled", func(t *testing.T) {
		cs := ssa.NewChangeSet()
		cs.Add(entry(a, ssa.DeletedAction))
		cs.Add(entry(b, ssa.SkippedAction))
		// c missing from the changeset → not settled → survivor.
		out := pruneSurvivors(objects, cs)
		g.Expect(out).To(HaveLen(1))
		g.Expect(out[0].GetName()).To(Equal("c"))
	})

	t.Run("unknown action means delete was rejected — must survive", func(t *testing.T) {
		cs := ssa.NewChangeSet()
		cs.Add(entry(a, ssa.DeletedAction))
		cs.Add(entry(b, ssa.UnknownAction)) // webhook denied / apiserver error
		cs.Add(entry(c, ssa.UnknownAction))
		out := pruneSurvivors(objects, cs)
		names := []string{}
		for _, o := range out {
			names = append(names, o.GetName())
		}
		g.Expect(names).To(ConsistOf("b", "c"))
	})

	t.Run("empty input is empty output", func(t *testing.T) {
		g.Expect(pruneSurvivors(nil, nil)).To(BeEmpty())
		g.Expect(pruneSurvivors([]*unstructured.Unstructured{}, ssa.NewChangeSet())).To(BeEmpty())
	})
}
