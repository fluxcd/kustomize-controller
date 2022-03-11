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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/aes"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
	"go.mozilla.org/sops/v3/keyservice"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/resource"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/kustomize-controller/internal/sops/azkv"
	intkeyservice "github.com/fluxcd/kustomize-controller/internal/sops/keyservice"
)

const (
	// DecryptionProviderSOPS is the SOPS provider name
	DecryptionProviderSOPS = "sops"
	// DecryptionVaultTokenFileName is the name of the file containing the Vault token
	DecryptionVaultTokenFileName = "sops.vault-token"
	// DecryptionAzureAuthFile is the Azure authentication file
	DecryptionAzureAuthFile = "sops.azure-kv"
)

type KustomizeDecryptor struct {
	client.Client

	kustomization  kustomizev1.Kustomization
	homeDir        string
	ageIdentities  []string
	vaultToken     string
	azureAADConfig *azkv.AADConfig
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
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("decryptor-%s-", kustomization.Name))
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
			data, err := kd.DataWithFormat(out, formats.Yaml, formats.Yaml)
			if err != nil {
				return nil, fmt.Errorf("DataWithFormat: %w", err)
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
					outputFormat := formats.FormatForPath(key)
					out, err := kd.DataWithFormat(data, formats.Yaml, outputFormat)
					if err != nil {
						return nil, fmt.Errorf("DataWithFormat: %w", err)
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

		tmpDir, err := os.MkdirTemp("", kd.kustomization.Name)
		if err != nil {
			return fmt.Errorf("tmp dir error: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		var ageIdentities []string
		var vaultToken string
		for name, value := range secret.Data {
			if name == DecryptionAzureAuthFile {
				azureConf := azkv.AADConfig{}
				if err = azkv.LoadAADConfigFromBytes(value, &azureConf); err != nil {
					return err
				}
				kd.azureAADConfig = &azureConf
				continue
			}
			switch filepath.Ext(name) {
			case ".asc":
				keyPath, err := securejoin.SecureJoin(tmpDir, name)
				if err != nil {
					return err
				}
				if err := os.WriteFile(keyPath, value, os.ModePerm); err != nil {
					return fmt.Errorf("unable to write key to storage: %w", err)
				}
				if err := kd.gpgImport(keyPath); err != nil {
					return err
				}
			case ".agekey":
				ageIdentities = append(ageIdentities, string(value))
			case ".vault-token":
				// Make sure we have the absolute file name
				if name == DecryptionVaultTokenFileName {
					token := string(value)
					token = strings.Trim(strings.TrimSpace(token), "\n")
					vaultToken = token
				}
			}
		}

		kd.ageIdentities = ageIdentities
		kd.vaultToken = vaultToken
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

func (kd *KustomizeDecryptor) decryptDotEnvFiles(dirpath string) error {
	kustomizePath := filepath.Join(dirpath, konfig.DefaultKustomizationFileName())
	ksData, err := os.ReadFile(kustomizePath)
	if err != nil {
		return nil
	}

	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	if err := yaml.Unmarshal(ksData, &kus); err != nil {
		return err
	}

	// recursively decrypt .env files in directories in
	for _, rsrc := range kus.Resources {
		rsrcPath := filepath.Join(dirpath, rsrc)
		isDir, err := isDir(rsrcPath)
		if err == nil && isDir {
			err := kd.decryptDotEnvFiles(rsrcPath)
			if err != nil {
				return fmt.Errorf("error decrypting .env files in dir '%s': %w",
					rsrcPath, err)
			}
		}
	}

	secretGens := kus.SecretGenerator
	for _, gen := range secretGens {
		for _, envFile := range gen.EnvSources {

			envFileParts := strings.Split(envFile, "=")
			if len(envFileParts) > 1 {
				envFile = envFileParts[1]
			}

			envPath := filepath.Join(dirpath, envFile)
			data, err := os.ReadFile(envPath)
			if err != nil {
				return err
			}

			if bytes.Contains(data, []byte("sops_mac=ENC[")) {
				out, err := kd.DataWithFormat(data, formats.Dotenv, formats.Dotenv)
				if err != nil {
					return err
				}

				err = os.WriteFile(envPath, out, 0644)
				if err != nil {
					return fmt.Errorf("error writing to file: %w", err)
				}
			}
		}
	}

	return nil
}

func (kd KustomizeDecryptor) DataWithFormat(data []byte, inputFormat, outputFormat formats.Format) ([]byte, error) {
	store := common.StoreForFormat(inputFormat)

	tree, err := store.LoadEncryptedFile(data)
	if err != nil {
		return nil, fmt.Errorf("LoadEncryptedFile: %w", err)
	}

	metadataKey, err := tree.Metadata.GetDataKeyWithKeyServices(
		[]keyservice.KeyServiceClient{
			intkeyservice.NewLocalClient(intkeyservice.NewServer(false, kd.homeDir, kd.vaultToken, kd.ageIdentities, kd.azureAADConfig)),
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

	outputStore := common.StoreForFormat(outputFormat)

	out, err := outputStore.EmitPlainFile(tree.Branches)
	if err != nil {
		return nil, fmt.Errorf("EmitPlainFile: %w", err)
	}

	return out, err
}

func isDir(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	return fileInfo.IsDir(), nil
}

// IsEncryptedSecret checks if the given object is a Kubernetes Secret encrypted with Mozilla SOPS.
func IsEncryptedSecret(object *unstructured.Unstructured) bool {
	if object.GetKind() == "Secret" && object.GetAPIVersion() == "v1" {
		if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "sops"); found {
			return true
		}
	}
	return false
}
