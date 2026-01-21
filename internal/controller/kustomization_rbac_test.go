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
	"fmt"
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_AppliesRoleAndRoleBinding(t *testing.T) {
	g := NewWithT(t)
	id := "custom-stage-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// Create a ServiceAccount with limited RBAC (not cluster-admin).
	// The SA can create Roles and RoleBindings, and has the permissions
	// that the test Role will grant (configmaps get/list).
	sa := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
	}
	g.Expect(k8sClient.Create(context.Background(), &sa)).To(Succeed())

	cr := rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"rbac.authorization.k8s.io"},
				Resources: []string{"roles", "rolebindings"},
				Verbs:     []string{"create", "update", "delete", "get", "list", "watch", "patch"},
			},
			// Grant the same permissions that the test Role will grant,
			// so RBAC escalation prevention allows creating the Role and
			// RoleBinding.
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list"},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), &cr)).To(Succeed())

	crb := rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     cr.Name,
		},
	}
	g.Expect(k8sClient.Create(context.Background(), &crb)).To(Succeed())

	// Manifests contain a Role and RoleBinding that references the Role.
	// Without CustomStageKinds, the RoleBinding will fail to apply because
	// the Role doesn't exist yet (both are sorted in the same stage).
	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "role.yaml",
				Body: fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: %[1]s-role
  namespace: %[1]s
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list"]
`, name),
			},
			{
				Name: "rolebinding.yaml",
				Body: fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[1]s-rolebinding
  namespace: %[1]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: %[1]s-role
subjects:
- kind: ServiceAccount
  name: default
  namespace: %[1]s
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Path:     "./",
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
					Name: "kubeconfig",
				},
			},
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      repositoryName.Name,
				Namespace: repositoryName.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			TargetNamespace:    id,
			ServiceAccountName: id, // Impersonate the limited SA
			Prune:              true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	readyCondition := &metav1.Condition{}

	t.Run("fails to reconcile Role and RoleBinding without custom stage", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return readyCondition != nil && readyCondition.Reason == meta.ReconciliationFailedReason
		}, timeout, time.Second).Should(BeTrue())

		// The error message format is:
		// "RoleBinding/<namespace>/<name>-rolebinding not found: rolebindings.rbac.authorization.k8s.io \"<name>-role\" not found"
		// This proves that:
		// 1. The involved object is the RoleBinding (dry-run failed on it)
		// 2. The referenced Role was not found during dry-run validation
		expectedMsg := fmt.Sprintf(
			"RoleBinding/%[1]s/%[1]s-rolebinding not found: rolebindings.rbac.authorization.k8s.io %q not found",
			id, id+"-role")
		g.Expect(readyCondition.Message).To(HavePrefix(expectedMsg))
	})

	t.Run("reconciles Role and RoleBinding with custom stage", func(t *testing.T) {
		// Capture the current CustomStageKinds
		originalCustomStageKinds := reconciler.CustomStageKinds
		t.Cleanup(func() {
			reconciler.CustomStageKinds = originalCustomStageKinds
		})

		// Set CustomStageKinds to include Role
		reconciler.CustomStageKinds = map[schema.GroupKind]struct{}{
			{Group: "rbac.authorization.k8s.io", Kind: "Role"}: {},
		}

		// Trigger reconciliation by updating the revision
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(readyCondition.Reason).To(Equal(meta.ReconciliationSucceededReason))

		// Verify the Role and RoleBinding were created
		role := &rbacv1.Role{}
		err = k8sClient.Get(context.Background(), types.NamespacedName{Name: id + "-role", Namespace: id}, role)
		g.Expect(err).NotTo(HaveOccurred())

		roleBinding := &rbacv1.RoleBinding{}
		err = k8sClient.Get(context.Background(), types.NamespacedName{Name: id + "-rolebinding", Namespace: id}, roleBinding)
		g.Expect(err).NotTo(HaveOccurred())
	})
}
