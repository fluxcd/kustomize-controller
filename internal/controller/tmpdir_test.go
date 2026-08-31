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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
)

func TestListStaleTempDirs(t *testing.T) {
	g := NewWithT(t)
	root := t.TempDir()

	stale := []string{
		filepath.Join(root, TempDirPrefix+"1931908412"),
		filepath.Join(root, TempDirPrefix+"252548195"),
	}
	for _, dir := range stale {
		g.Expect(os.MkdirAll(filepath.Join(dir, "nested"), 0o700)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(dir, "nested", "deployment.yaml"), []byte("kind: Deployment"), 0o600)).To(Succeed())
	}

	// Entries that must be left alone: a directory with another prefix
	// and a regular file that happens to carry the prefix.
	g.Expect(os.Mkdir(filepath.Join(root, "other-123"), 0o700)).To(Succeed())
	g.Expect(os.WriteFile(filepath.Join(root, TempDirPrefix+"file"), []byte("x"), 0o600)).To(Succeed())

	dirs, err := ListStaleTempDirs(root)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(dirs).To(ConsistOf(stale))

	_, err = ListStaleTempDirs(filepath.Join(root, "missing"))
	g.Expect(err).To(HaveOccurred())
}

func TestPurgeTempDirs(t *testing.T) {
	newDirs := func(g *WithT) (string, []string) {
		root := t.TempDir()
		dirs := []string{
			filepath.Join(root, TempDirPrefix+"1931908412"),
			filepath.Join(root, TempDirPrefix+"252548195"),
		}
		for _, dir := range dirs {
			g.Expect(os.MkdirAll(filepath.Join(dir, "nested"), 0o700)).To(Succeed())
			g.Expect(os.WriteFile(filepath.Join(dir, "nested", "deployment.yaml"), []byte("kind: Deployment"), 0o600)).To(Succeed())
		}
		return root, dirs
	}

	t.Run("removes listed dirs only", func(t *testing.T) {
		g := NewWithT(t)
		root, dirs := newDirs(g)
		keep := filepath.Join(root, TempDirPrefix+"698526464")
		g.Expect(os.Mkdir(keep, 0o700)).To(Succeed())

		g.Expect(PurgeTempDirs(context.Background(), logr.Discard(), dirs)).To(Equal(len(dirs)))

		for _, dir := range dirs {
			g.Expect(dir).NotTo(BeADirectory())
		}
		g.Expect(keep).To(BeADirectory())
	})

	t.Run("tolerates already removed dirs", func(t *testing.T) {
		g := NewWithT(t)
		_, dirs := newDirs(g)
		g.Expect(os.RemoveAll(dirs[0])).To(Succeed())

		g.Expect(PurgeTempDirs(context.Background(), logr.Discard(), dirs)).To(Equal(len(dirs)))
		for _, dir := range dirs {
			g.Expect(dir).NotTo(BeADirectory())
		}
	})

	t.Run("stops when context is done", func(t *testing.T) {
		g := NewWithT(t)
		_, dirs := newDirs(g)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		g.Expect(PurgeTempDirs(ctx, logr.Discard(), dirs)).To(BeZero())
		for _, dir := range dirs {
			g.Expect(dir).To(BeADirectory())
		}
	})
}
