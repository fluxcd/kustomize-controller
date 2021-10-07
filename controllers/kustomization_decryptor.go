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
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	securejoin "github.com/cyphar/filepath-securejoin"
	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/aes"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
	"go.mozilla.org/sops/v3/keyservice"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/yaml"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	intkeyservice "github.com/fluxcd/kustomize-controller/internal/sops/keyservice"
)

const DecryptionProviderSOPS = "sops"

type KustomizeDecryptor struct {
	client.Client
	kustomization kustomizev1.Kustomization
	homeDir       string
	ageIdentities []string
}

func NewDecryptor(kubeClient client.Client,
	kustomization kustomizev1.Kustomization, homeDir string) *KustomizeDecryptor {
	return &KustomizeDecryptor{
		Client:        kubeClient,
		kustomization: kustomization,
		homeDir:       homeDir,
	}
}

func NewTempDecryptor(kubeClient client.Client,
	kustomization kustomizev1.Kustomization) (*KustomizeDecryptor, func(), error) {
	tmpDir, err := ioutil.TempDir("", fmt.Sprintf("decryptor-%s-", kustomization.Name))
	if err != nil {
		return nil, nil, fmt.Errorf("tmp dir error: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }
	return NewDecryptor(kubeClient, kustomization, tmpDir), cleanup, nil
}

func (kd *KustomizeDecryptor) Decrypt(res *resource.Resource) (*resource.Resource, error) {
	out, err := res.AsYAML()
	if err != nil {
		return nil, err
	}

	if kd.kustomization.Spec.Decryption != nil && kd.kustomization.Spec.Decryption.Provider == DecryptionProviderSOPS {

		if bytes.Contains(out, []byte("sops:")) && bytes.Contains(out, []byte("mac: ENC[")) {
			store := common.StoreForFormat(formats.Yaml)

			tree, err := store.LoadEncryptedFile(out)
			if err != nil {
				return nil, fmt.Errorf("LoadEncryptedFile: %w", err)
			}

			key, err := tree.Metadata.GetDataKeyWithKeyServices(
				[]keyservice.KeyServiceClient{
					intkeyservice.NewLocalClient(intkeyservice.NewServer(false, kd.homeDir, kd.ageIdentities)),
				},
			)
			if err != nil {
				if userErr, ok := err.(sops.UserError); ok {
					err = fmt.Errorf(userErr.UserError())
				}
				return nil, fmt.Errorf("GetDataKey: %w", err)
			}

			cipher := aes.NewCipher()
			if _, err := tree.Decrypt(key, cipher); err != nil {
				return nil, fmt.Errorf("AES decrypt: %w", err)
			}

			data, err := store.EmitPlainFile(tree.Branches)
			if err != nil {
				return nil, fmt.Errorf("EmitPlainFile: %w", err)
			}

			jsonData, err := yaml.YAMLToJSON(data)
			if err != nil {
				return nil, fmt.Errorf("YAMLToJSON: %w", err)
			}

			err = res.UnmarshalJSON(jsonData)
			if err != nil {
				return nil, fmt.Errorf("UnmarshalJSON: %w", err)
			}
			return res, nil

		} else if res.GetKind() == "Secret" {

			dataMap := res.GetDataMap()

			for key, value := range dataMap {

				data, err := base64.StdEncoding.DecodeString(value)
				if err != nil {
					fmt.Println("Base64 Decode: %w", err)
				}

				if bytes.Contains(data, []byte("sops")) && bytes.Contains(data, []byte("ENC[")) {

					store := common.StoreForFormat(formats.Yaml)

					tree, err := store.LoadEncryptedFile(data)
					if err != nil {
						return nil, fmt.Errorf("LoadEncryptedFile: %w", err)
					}

					metadataKey, err := tree.Metadata.GetDataKeyWithKeyServices(
						[]keyservice.KeyServiceClient{
							intkeyservice.NewLocalClient(intkeyservice.NewServer(false, kd.homeDir, kd.ageIdentities)),
						},
					)

					if err != nil {
						if userErr, ok := err.(sops.UserError); ok {
							err = fmt.Errorf(userErr.UserError())
						}
						return nil, fmt.Errorf("GetDataKey: %w", err)
					}

					cipher := aes.NewCipher()
					if _, err := tree.Decrypt(metadataKey, cipher); err != nil {
						return nil, fmt.Errorf("AES decrypt: %w", err)
					}

					outputStore := common.DefaultStoreForPath(key)

					out, err := outputStore.EmitPlainFile(tree.Branches)
					if err != nil {
						return nil, fmt.Errorf("EmitPlainFile: %w", err)
					}

					dataMap[key] = base64.StdEncoding.EncodeToString(out)
				}
			}

			res.SetDataMap(dataMap)

			return res, nil

		}
	}
	return nil, nil
}

func (kd *KustomizeDecryptor) ImportKeys(ctx context.Context) error {
	if kd.kustomization.Spec.Decryption != nil && kd.kustomization.Spec.Decryption.SecretRef != nil {
		secretName := types.NamespacedName{
			Namespace: kd.kustomization.GetNamespace(),
			Name:      kd.kustomization.Spec.Decryption.SecretRef.Name,
		}

		var secret corev1.Secret
		if err := kd.Get(ctx, secretName, &secret); err != nil {
			return fmt.Errorf("decryption secret error: %w", err)
		}

		tmpDir, err := ioutil.TempDir("", kd.kustomization.Name)
		if err != nil {
			return fmt.Errorf("tmp dir error: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		var ageIdentities []string
		for name, file := range secret.Data {
			switch filepath.Ext(name) {
			case ".asc":
				keyPath, err := securejoin.SecureJoin(tmpDir, name)
				if err != nil {
					return err
				}
				if err := ioutil.WriteFile(keyPath, file, os.ModePerm); err != nil {
					return fmt.Errorf("unable to write key to storage: %w", err)
				}
				if err := kd.gpgImport(keyPath); err != nil {
					return err
				}
			case ".agekey":
				ageIdentities = append(ageIdentities, string(file))
			}
		}
		kd.ageIdentities = ageIdentities
	}

	return nil
}

func (kd *KustomizeDecryptor) gpgImport(path string) error {
	args := []string{"--batch", "--import", path}
	if kd.homeDir != "" {
		args = append([]string{"--homedir", kd.homeDir}, args...)
	}
	cmd := exec.Command("gpg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gpg import error: %s", string(out))
	}
	return nil
}
