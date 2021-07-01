/*
Copyright 2020 The Flux authors

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
	"errors"
	"fmt"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os/exec"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

// parseApplyOutput extracts the objects and the action
// performed by kubectl e.g.:
// service/backend created
// service/frontend configured
// service/database unchanged
func parseApplyOutput(in []byte) map[string]string {
	result := make(map[string]string)
	input := strings.Split(string(in), "\n")
	if len(input) == 0 {
		return result
	}
	var parts []string
	for _, str := range input {
		if str != "" {
			parts = append(parts, str)
		}
	}
	for _, str := range parts {
		kv := strings.Split(str, " ")
		if len(kv) > 1 {
			result[kv[0]] = kv[1]
		}
	}
	return result
}

// parasDiffOutput extracts the objects and the action
// performed by kubectl diff e.g. :
//diff -u -N /tmp/LIVE-090729454/v1.ConfigMap.default.first /tmp/MERGED-357794933/v1.ConfigMap.default.first
//--- /tmp/LIVE-090729454/v1.ConfigMap.default.first	2021-06-29 05:52:39.456678181 +0200
//+++ /tmp/MERGED-357794933/v1.ConfigMap.default.first	2021-06-29 05:52:39.461678214 +0200
// @@ -8,7 +8,7 @@
// ...
// diff -u -N /tmp/LIVE-656588208/kustomize.toolkit.fluxcd.io.v1beta1.Kustomization.namespace.name /tmp/MERGED-532750671/kustomize.toolkit.fluxcd.io.v1beta1.Kustomization.namespace.name
// --- /tmp/LIVE-656588208/kustomize.toolkit.fluxcd.io.v1beta1.Kustomization.namespace.name	2021-06-07 12:58:20.738794982 +0200
// +++ /tmp/MERGED-532750671/kustomize.toolkit.fluxcd.io.v1beta1.Kustomization.namespace.name	2021-06-07 12:58:20.798795908 +0200
// @@ -0,0 +1,36 @@
func parseDiffOutput(in []byte) map[string]string {
	result := make(map[string]string)
	var resourcename string
	var action string

	input := strings.Split(string(in), "diff -u -N")
	if len(input) == 0 {
		return result
	}

	for _, str := range input {
		stringSlice := strings.Split(str, "\n")
		s := strings.Split(stringSlice[0], "/")
		resourcename = s[len(s)-1]

		if containsChangeInSlice(stringSlice) {
			action = "configured"
		} else {
			action = "unchanged"
		}
		if strings.Contains(str, "@@ -0,0") {
			action = "created"
		}
		if resourcename != "" {
			result[resourcename] = action
		}
	}

	return result
}

// parseApplyError extracts the errors from the kubectl
// apply output by removing the successfully applied objects
func parseApplyError(in []byte) string {
	errors := ""
	lines := strings.Split(string(in), "\n")
	for _, line := range lines {
		if line != "" &&
			!strings.HasSuffix(line, "created") &&
			!strings.HasSuffix(line, "created (dry run)") &&
			!strings.HasSuffix(line, "created (server dry run)") &&
			!strings.HasSuffix(line, "configured") &&
			!strings.HasSuffix(line, "configured (dry run)") &&
			!strings.HasSuffix(line, "configured (server dry run)") &&
			!strings.HasSuffix(line, "unchanged") &&
			!strings.HasSuffix(line, "unchanged (dry run)") &&
			!strings.HasSuffix(line, "unchanged (server dry run)") {
			errors += line + "\n"
		}
	}

	return errors
}

func execApply(ctx context.Context, cmd string) ([]byte, error) {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	output, applyerr := command.CombinedOutput()
	if applyerr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return output, fmt.Errorf("apply timeout: %w", ctx.Err())
		}

		if string(output) == "" {
			return output, fmt.Errorf("apply failed: %w, kubectl process was killed, probably due to OOM", applyerr)
		}

		applyErr := parseApplyError(output)
		if applyErr == "" {
			applyErr = "no error output found, this may happen because of a timeout"
		}
		return output, fmt.Errorf("apply failed: %s", applyErr)
	}
	return output, nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// ObjectKey returns client.ObjectKey for the object.
func ObjectKey(object metav1.Object) client.ObjectKey {
	return client.ObjectKey{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}
}

func containsChangeInSlice(tmpslice []string) bool {
	checksumAnnotation := fmt.Sprintf("    %s/checksum:", kustomizev1.GroupVersion.Group)
	for _, s := range tmpslice {
		if strings.HasPrefix(s, "+"+checksumAnnotation) || strings.HasPrefix(s, "-"+checksumAnnotation) || strings.HasPrefix(s, "--- /tmp") || strings.HasPrefix(s, "+++ /tmp") {
			continue
		}
		if strings.Contains(s, "-  ") || strings.Contains(s, "+  ") {
			return true
		}
	}
	return false
}
