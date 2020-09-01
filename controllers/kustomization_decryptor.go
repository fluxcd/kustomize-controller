package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"

	"go.mozilla.org/sops/v3/aes"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/yaml"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

const DecryptionProviderSOPS = "sops"

type KustomizeDecryptor struct {
	client.Client
	kustomization kustomizev1.Kustomization
}

func NewDecryptor(kubeClient client.Client, kustomization kustomizev1.Kustomization) *KustomizeDecryptor {
	return &KustomizeDecryptor{
		Client:        kubeClient,
		kustomization: kustomization,
	}
}

func (kd *KustomizeDecryptor) Decrypt(res *resource.Resource) (*resource.Resource, error) {
	out, err := res.AsYAML()
	if err != nil {
		return nil, err
	}

	if kd.kustomization.Spec.Decryption != nil && kd.kustomization.Spec.Decryption.Provider == DecryptionProviderSOPS &&
		bytes.Contains(out, []byte("sops:")) && bytes.Contains(out, []byte("mac: ENC[")) {
		store := common.StoreForFormat(formats.Yaml)

		tree, err := store.LoadEncryptedFile(out)
		if err != nil {
			return nil, fmt.Errorf("LoadEncryptedFile: %w", err)
		}

		key, err := tree.Metadata.GetDataKey()
		if err != nil {
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

		for name, key := range secret.Data {
			keyPath := path.Join(tmpDir, name)
			if err := ioutil.WriteFile(keyPath, key, os.ModePerm); err != nil {
				return fmt.Errorf("unable to write key to storage: %w", err)
			}
			if err := kd.gpgImport(keyPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func (kd *KustomizeDecryptor) gpgImport(path string) error {
	cmd := exec.Command("gpg", "--import", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gpg import error: %s", string(out))
	}
	return nil
}
