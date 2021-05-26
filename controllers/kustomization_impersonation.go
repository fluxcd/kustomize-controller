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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	pkgclient "github.com/fluxcd/pkg/runtime/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

type KustomizeImpersonation struct {
	workdir                 string
	enableUserImpersonation bool
	kustomization           kustomizev1.Kustomization
	statusPoller            *polling.StatusPoller
	client.Client
}

func NewKustomizeImpersonation(
	kustomization kustomizev1.Kustomization,
	enableUserImpersonation bool,
	kubeClient client.Client,
	statusPoller *polling.StatusPoller,
	workdir string) *KustomizeImpersonation {
	return &KustomizeImpersonation{
		workdir:                 workdir,
		kustomization:           kustomization,
		statusPoller:            statusPoller,
		Client:                  kubeClient,
		enableUserImpersonation: enableUserImpersonation,
	}
}

// GetClient creates a controller-runtime client for talking to a Kubernetes API server.
// If KubeConfig is set, will use the kubeconfig bytes from the Kubernetes secret.
// If ServiceAccountName is set, will use the cluster provided kubeconfig impersonating the SA.
// If --kubeconfig is set, will use the kubeconfig file at that location.
// Otherwise will assume running in cluster and use the cluster provided kubeconfig.
func (ki *KustomizeImpersonation) GetClient(ctx context.Context) (client.Client, *polling.StatusPoller, error) {
	clientgenOpts := pkgclient.ImpersonationConfig{
		Namespace: ki.kustomization.Namespace,
		Enabled:   ki.enableUserImpersonation,
	}

	if ki.kustomization.Spec.Principal == nil && ki.kustomization.Spec.ServiceAccountName != "" ||
		!ki.enableUserImpersonation && ki.kustomization.Spec.ServiceAccountName != "" {
		clientgenOpts.Name = ki.kustomization.Spec.ServiceAccountName
		clientgenOpts.Kind = pkgclient.ServiceAccountType
		clientgenOpts.Enabled = false
	} else if ki.kustomization.Spec.Principal != nil {
		clientgenOpts.Name = ki.kustomization.Spec.Principal.Name
		clientgenOpts.Kind = ki.kustomization.Spec.Principal.Kind
	}

	if ki.kustomization.Spec.KubeConfig == nil {
		return ki.clientForImpersonation(ctx, clientgenOpts)
	}

	clientgenOpts.KubeConfig = &pkgclient.KubeConfig{
		SecretRef: ki.kustomization.Spec.KubeConfig.SecretRef,
	}
	return ki.clientForKubeConfig(ctx, clientgenOpts)
}

func (ki *KustomizeImpersonation) clientForImpersonation(ctx context.Context, impConfig pkgclient.ImpersonationConfig) (client.Client, *polling.StatusPoller, error) {
	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, nil, err
	}

	restConfig, err = pkgclient.GetConfigForAccount(ctx, ki.Client, restConfig, impConfig)

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

func (ki *KustomizeImpersonation) clientForKubeConfig(ctx context.Context, impConfig pkgclient.ImpersonationConfig) (client.Client, *polling.StatusPoller, error) {
	secretName := types.NamespacedName{
		Namespace: ki.kustomization.GetNamespace(),
		Name:      ki.kustomization.Spec.KubeConfig.SecretRef.Name,
	}

	kubeConfigBytes, err := pkgclient.GetKubeConfigFromSecret(ctx, ki.Client, secretName)

	if err != nil {
		return nil, nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		return nil, nil, err
	}

	// Only impersonate if user impersonation is enabled and the principal is set
	if impConfig.Enabled && impConfig.Kind != "" && impConfig.Name != "" {
		restConfig, err = pkgclient.GetConfigForAccount(ctx, ki.Client, restConfig, impConfig)
		if err != nil {
			return nil, nil, err
		}
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

	kubeConfig, err := pkgclient.GetKubeConfigFromSecret(ctx, ki.Client, secretName)
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
