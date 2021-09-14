/*
Copyright 2021 Stefan Prodan
Copyright 2021 The Flux authors

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

package ssa

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDiff(t *testing.T) {
	timeout := 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	id := generateName("diff")
	objects, err := readManifest("testdata/test1.yaml", id)
	if err != nil {
		t.Fatal(err)
	}

	configMapName, configMap := getFirstObject(objects, "ConfigMap", id)
	secretName, secret := getFirstObject(objects, "Secret", id)

	if err := unstructured.SetNestedField(secret.Object, false, "immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ApplyAllStaged(ctx, objects, false, timeout); err != nil {
		t.Fatal(err)
	}

	t.Run("generates empty diff for unchanged object", func(t *testing.T) {
		changeSetEntry, err := manager.Diff(ctx, configMap)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(configMapName, changeSetEntry.Subject); diff != "" {
			t.Errorf("Mismatch from expected value (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff(string(UnchangedAction), changeSetEntry.Action); diff != "" {
			t.Errorf("Mismatch from expected value (-want +got):\n%s", diff)
		}
	})

	t.Run("generates diff for changed object", func(t *testing.T) {
		newVal := "diff-test"
		err = unstructured.SetNestedField(configMap.Object, newVal, "data", "key")
		if err != nil {
			t.Fatal(err)
		}

		changeSetEntry, err := manager.Diff(ctx, configMap)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(string(ConfiguredAction), changeSetEntry.Action); diff != "" {
			t.Errorf("Mismatch from expected value (-want +got):\n%s", diff)
		}

		if !strings.Contains(changeSetEntry.Diff, newVal) {
			t.Errorf("Mismatch from expected value, want %s", newVal)
		}
	})

	t.Run("masks secret values", func(t *testing.T) {
		newVal := "diff-test"
		err = unstructured.SetNestedField(secret.Object, newVal, "stringData", "key")
		if err != nil {
			t.Fatal(err)
		}

		newKey := "key.new"
		err = unstructured.SetNestedField(secret.Object, newVal, "stringData", newKey)
		if err != nil {
			t.Fatal(err)
		}

		changeSetEntry, err := manager.Diff(ctx, secret)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(secretName, changeSetEntry.Subject); diff != "" {
			t.Errorf("Mismatch from expected value (-want +got):\n%s", diff)
		}

		if !strings.Contains(changeSetEntry.Diff, newKey) {
			t.Errorf("Mismatch from expected value, got %s", changeSetEntry.Diff)
		}

		if strings.Contains(changeSetEntry.Diff, newVal) {
			t.Errorf("Mismatch from expected value, got %s", changeSetEntry.Diff)
		}
	})
}
