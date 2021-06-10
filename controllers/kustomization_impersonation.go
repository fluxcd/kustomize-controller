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
	"io/ioutil"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

type KustomizeImpersonation struct {
	workdir       string
	kustomization *kustomizev1.Kustomization
	statusPoller  *polling.StatusPoller
	client.Client
}

func NewKustomizeImpersonation(
	kustomization *kustomizev1.Kustomization,
	kubeClient client.Client,
	statusPoller *polling.StatusPoller,
	workdir string) *KustomizeImpersonation {
	return &KustomizeImpersonation{
		workdir:       workdir,
		kustomization: kustomization,
		statusPoller:  statusPoller,
		Client:        kubeClient,
	}
}

func (ki *KustomizeImpersonation) GetServiceAccountToken(ctx context.Context) (string, error) {
	namespacedName := types.NamespacedName{
		Namespace: ki.kustomization.Namespace,
		Name:      ki.kustomization.Spec.ServiceAccountName,
	}

	var serviceAccount corev1.ServiceAccount
	err := ki.Client.Get(ctx, namespacedName, &serviceAccount)
	if err != nil {
		return "", err
	}

	secretName := types.NamespacedName{
		Namespace: ki.kustomization.Namespace,
		Name:      ki.kustomization.Spec.ServiceAccountName,
	}

	for _, secret := range serviceAccount.Secrets {
		if strings.HasPrefix(secret.Name, fmt.Sprintf("%s-token", serviceAccount.Name)) {
			secretName.Name = secret.Name
			break
		}
	}

	var secret corev1.Secret
	err = ki.Client.Get(ctx, secretName, &secret)
	if err != nil {
		return "", err
	}

	var token string
	if data, ok := secret.Data["token"]; ok {
		token = string(data)
	} else {
		return "", fmt.Errorf("the service account secret '%s' does not containt a token", secretName.String())
	}

	return token, nil
}

// GetClient creates a controller-runtime client for talking to a Kubernetes API server.
// If KubeConfig is set, will use the kubeconfig bytes from the Kubernetes secret.
// If ServiceAccountName is set, will use the cluster provided kubeconfig impersonating the SA.
// If --kubeconfig is set, will use the kubeconfig file at that location.
// Otherwise will assume running in cluster and use the cluster provided kubeconfig.
func (ki *KustomizeImpersonation) GetClient(ctx context.Context) (client.Client, *polling.StatusPoller, error) {
	if ki.kustomization.Spec.KubeConfig == nil {
		if ki.kustomization.Spec.ServiceAccountName != "" {
			return ki.clientForServiceAccount(ctx)
		}

		return ki.Client, ki.statusPoller, nil
	}
	return ki.clientForKubeConfig(ctx)
}

func (ki *KustomizeImpersonation) clientForServiceAccount(ctx context.Context) (client.Client, *polling.StatusPoller, error) {
	token, err := ki.GetServiceAccountToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, nil, err
	}
	restConfig.BearerToken = token
	restConfig.BearerTokenFile = "" // Clear, as it overrides BearerToken

	restMapper, err := apiutil.NewDynamicRESTMapper(restConfig)
	if err != nil {
		return nil, nil, err
	}

	client, err := client.New(restConfig, client.Options{Mapper: restMapper})
	if err != nil {
		return nil, nil, err
	}

	statusPoller := polling.NewStatusPoller(client, restMapper)
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

	restMapper, err := apiutil.NewDynamicRESTMapper(restConfig)
	if err != nil {
		return nil, nil, err
	}

	client, err := client.New(restConfig, client.Options{Mapper: restMapper})
	if err != nil {
		return nil, nil, err
	}

	statusPoller := polling.NewStatusPoller(client, restMapper)

	return client, statusPoller, err
}

func (ki *KustomizeImpersonation) WriteKubeConfig(ctx context.Context) (string, error) {
	secretName := types.NamespacedName{
		Namespace: ki.kustomization.GetNamespace(),
		Name:      ki.kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	kubeConfig, err := ki.getKubeConfig(ctx)
	if err != nil {
		return "", err
	}

	f, err := ioutil.TempFile(ki.workdir, "kubeconfig")
	defer f.Close()
	if err != nil {
		return "", fmt.Errorf("unable to write KubeConfig secret '%s' to storage: %w", secretName.String(), err)
	}
	if _, err := f.Write(kubeConfig); err != nil {
		return "", fmt.Errorf("unable to write KubeConfig secret '%s' to storage: %w", secretName.String(), err)
	}
	return f.Name(), nil
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

	kubeConfig, ok := secret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("KubeConfig secret '%s' doesn't contain a 'value' key ", secretName.String())
	}

	return kubeConfig, nil
}
