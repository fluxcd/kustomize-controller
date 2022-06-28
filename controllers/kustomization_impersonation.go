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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"

	runtimeClient "github.com/fluxcd/pkg/runtime/client"
)

// KustomizeImpersonation holds the state for impersonating a service account.
type KustomizeImpersonation struct {
	client.Client
	kustomization         kustomizev1.Kustomization
	statusPoller          *polling.StatusPoller
	defaultServiceAccount string
	pollingOpts           polling.Options
	kubeConfigOpts        runtimeClient.KubeConfigOptions
}

// NewKustomizeImpersonation creates a new KustomizeImpersonation.
func NewKustomizeImpersonation(
	kustomization kustomizev1.Kustomization,
	kubeClient client.Client,
	statusPoller *polling.StatusPoller,
	defaultServiceAccount string,
	kubeConfigOpts runtimeClient.KubeConfigOptions,
	pollingOpts polling.Options) *KustomizeImpersonation {
	return &KustomizeImpersonation{
		defaultServiceAccount: defaultServiceAccount,
		kustomization:         kustomization,
		statusPoller:          statusPoller,
		Client:                kubeClient,
		kubeConfigOpts:        kubeConfigOpts,
		pollingOpts:           pollingOpts,
	}
}

// GetClient creates a controller-runtime client for talking to a Kubernetes API server.
// If spec.KubeConfig is set, use the kubeconfig bytes from the Kubernetes secret.
// Otherwise will assume running in cluster and use the cluster provided kubeconfig.
// If a --default-service-account is set and no spec.ServiceAccountName, use the provided kubeconfig and impersonate the default SA.
// If spec.ServiceAccountName is set, use the provided kubeconfig and impersonate the specified SA.
func (ki *KustomizeImpersonation) GetClient(ctx context.Context) (client.Client, *polling.StatusPoller, error) {
	switch {
	case ki.kustomization.Spec.KubeConfig != nil:
		return ki.clientForKubeConfig(ctx)
	case ki.defaultServiceAccount != "" || ki.kustomization.Spec.ServiceAccountName != "":
		return ki.clientForServiceAccountOrDefault()
	default:
		return ki.Client, ki.statusPoller, nil
	}
}

// CanFinalize asserts if the given Kustomization can be finalized using impersonation.
func (ki *KustomizeImpersonation) CanFinalize(ctx context.Context) bool {
	name := ki.defaultServiceAccount
	if sa := ki.kustomization.Spec.ServiceAccountName; sa != "" {
		name = sa
	}
	if name == "" {
		return true
	}

	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ki.kustomization.Namespace,
		},
	}
	if err := ki.Client.Get(ctx, client.ObjectKeyFromObject(sa), sa); err != nil {
		return false
	}

	return true
}

func (ki *KustomizeImpersonation) setImpersonationConfig(restConfig *rest.Config) {
	name := ki.defaultServiceAccount
	if sa := ki.kustomization.Spec.ServiceAccountName; sa != "" {
		name = sa
	}
	if name != "" {
		username := fmt.Sprintf("system:serviceaccount:%s:%s", ki.kustomization.GetNamespace(), name)
		restConfig.Impersonate = rest.ImpersonationConfig{UserName: username}
	}
}

func (ki *KustomizeImpersonation) clientForServiceAccountOrDefault() (client.Client, *polling.StatusPoller, error) {
	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, nil, err
	}
	ki.setImpersonationConfig(restConfig)

	restMapper, err := apiutil.NewDynamicRESTMapper(restConfig)
	if err != nil {
		return nil, nil, err
	}

	client, err := client.New(restConfig, client.Options{Mapper: restMapper})
	if err != nil {
		return nil, nil, err
	}

	statusPoller := polling.NewStatusPoller(client, restMapper, ki.pollingOpts)
	return client, statusPoller, err

}

func (ki *KustomizeImpersonation) clientForKubeConfig(ctx context.Context) (client.Client, *polling.StatusPoller, error) {
	kubeConfigBytes, err := ki.getKubeConfig(ctx)
	if err != nil {
		return nil, nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		return nil, nil, err
	}

	restConfig = runtimeClient.KubeConfig(restConfig, ki.kubeConfigOpts)
	ki.setImpersonationConfig(restConfig)

	restMapper, err := apiutil.NewDynamicRESTMapper(restConfig)
	if err != nil {
		return nil, nil, err
	}

	client, err := client.New(restConfig, client.Options{Mapper: restMapper})
	if err != nil {
		return nil, nil, err
	}

	statusPoller := polling.NewStatusPoller(client, restMapper, ki.pollingOpts)

	return client, statusPoller, err
}

func (ki *KustomizeImpersonation) getKubeConfig(ctx context.Context) ([]byte, error) {
	secretName := types.NamespacedName{
		Namespace: ki.kustomization.GetNamespace(),
		Name:      ki.kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	var secret corev1.Secret
	if err := ki.Get(ctx, secretName, &secret); err != nil {
		return nil, fmt.Errorf("unable to read KubeConfig secret '%s' error: %w", secretName.String(), err)
	}

	var kubeConfig []byte
	switch {
	case ki.kustomization.Spec.KubeConfig.SecretRef.Key != "":
		key := ki.kustomization.Spec.KubeConfig.SecretRef.Key
		kubeConfig = secret.Data[key]
		if kubeConfig == nil {
			return nil, fmt.Errorf("KubeConfig secret '%s' does not contain a '%s' key with a kubeconfig", secretName, key)
		}
	case secret.Data["value"] != nil:
		kubeConfig = secret.Data["value"]
	case secret.Data["value.yaml"] != nil:
		kubeConfig = secret.Data["value.yaml"]
	default:
		// User did not specify a key, and the 'value' key was not defined.
		return nil, fmt.Errorf("KubeConfig secret '%s' does not contain a 'value' key with a kubeconfig", secretName)
	}

	return kubeConfig, nil
}
