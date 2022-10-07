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
	"testing"

	. "github.com/onsi/gomega"
)

func Test_Buid(t *testing.T) {
	t.Run("remote build", func(t *testing.T) {
		g := NewWithT(t)

		_, err := Build("testdata/remote", "testdata/remote", true)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("no remote build", func(t *testing.T) {
		g := NewWithT(t)

		_, err := Build("testdata/remote", "testdata/remote", false)
		g.Expect(err).To(HaveOccurred())
	})
}

func Test_Buid_panic(t *testing.T) {
	t.Run("build panic", func(t *testing.T) {
		g := NewWithT(t)

		_, err := Build("testdata/panic", "testdata/panic", false)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("recovered from kustomize build panic"))
		// Run again to ensure the lock is released
		_, err = Build("testdata/panic", "testdata/panic", false)
		g.Expect(err).To(HaveOccurred())
	})
}

func Test_Buid_rel_basedir(t *testing.T) {
	g := NewWithT(t)

	_, err := Build("testdata/relbase", "testdata/relbase/clusters/staging/flux-system", false)
	g.Expect(err).ToNot(HaveOccurred())
}
