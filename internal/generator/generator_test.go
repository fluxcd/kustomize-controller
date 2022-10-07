/*
Copyright 2022 The Flux authors

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

package generator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kustypes "sigs.k8s.io/kustomize/api/types"

	"github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/kustomize"
	. "github.com/onsi/gomega"
)

func TestGenerator_WriteFile(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{
			name: "detects kustomization.yml",
			dir:  "yml",
		},
		{
			name: "detects Kustomization",
			dir:  "Kustomization",
		},
		{
			name: "detects kustomization.yaml",
			dir:  "yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			g.Expect(copy.Copy("./testdata/different-filenames", tmpDir)).To(Succeed())
			ks := v1beta2.Kustomization{
				Spec: v1beta2.KustomizationSpec{
					PatchesStrategicMerge: []apiextv1.JSON{
						{
							Raw: []byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"podinfo","labels":{"patch4":"strategic-merge"}}}`),
						},
					},
					Patches: []kustomize.Patch{
						{
							Patch: `- op: add
  path: /metadata/labels/patch1
  value: inline-json`,
							Target: kustomize.Selector{
								LabelSelector: "app=podinfo",
							},
						},
					},
					PatchesJSON6902: []kustomize.JSON6902Patch{
						{
							Patch: []kustomize.JSON6902{
								{Op: "add", Path: "/metadata/labels/patch3", Value: &apiextv1.JSON{Raw: []byte(`"json6902"`)}},
							},
						},
					},
				},
			}
			kfile, err := NewGenerator(filepath.Join(tmpDir, tt.dir), ks).WriteFile(filepath.Join(tmpDir, tt.dir))
			g.Expect(err).ToNot(HaveOccurred())

			kfileYAML, err := os.ReadFile(kfile)
			g.Expect(err).ToNot(HaveOccurred())
			var k kustypes.Kustomization
			g.Expect(k.Unmarshal(kfileYAML)).To(Succeed())
			g.Expect(k.Patches).To(HaveLen(1), "unexpected number of patches in kustomization file")
			g.Expect(k.PatchesStrategicMerge).To(HaveLen(1), "unexpected number of strategic merge patches in kustomization file")
			g.Expect(k.PatchesJson6902).To(HaveLen(1), "unexpected number of RFC 6902 patches in kustomization file")
		})
	}
}
