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
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/k8sdeps/kunstruct"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	"github.com/fluxcd/pkg/apis/kustomize"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

const (
	transformerFileName = "kustomization-gc-labels.yaml"
)

type KustomizeGenerator struct {
	kustomization *kustomizev1.Kustomization
	client.Client
}

func NewGenerator(kustomization *kustomizev1.Kustomization, kubeClient client.Client) *KustomizeGenerator {
	return &KustomizeGenerator{
		kustomization: kustomization,
		Client:        kubeClient,
	}
}

func (kg *KustomizeGenerator) WriteFile(ctx context.Context, dirPath string) (string, error) {
	kfile := filepath.Join(dirPath, konfig.DefaultKustomizationFileName())

	checksum, err := kg.checksum(ctx, dirPath)
	if err != nil {
		return "", err
	}

	if err := kg.generateLabelTransformer(checksum, dirPath); err != nil {
		return "", err
	}

	data, err := ioutil.ReadFile(kfile)
	if err != nil {
		return "", err
	}

	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	if err := yaml.Unmarshal(data, &kus); err != nil {
		return "", err
	}

	if len(kus.Transformers) == 0 {
		kus.Transformers = []string{transformerFileName}
	} else {
		var exists bool
		for _, transformer := range kus.Transformers {
			if transformer == transformerFileName {
				exists = true
				break
			}
		}
		if !exists {
			kus.Transformers = append(kus.Transformers, transformerFileName)
		}
	}

	if kg.kustomization.Spec.TargetNamespace != "" {
		kus.Namespace = kg.kustomization.Spec.TargetNamespace
	}

	for _, m := range kg.kustomization.Spec.PatchesStrategicMerge {
		kus.PatchesStrategicMerge = append(kus.PatchesStrategicMerge, kustypes.PatchStrategicMerge(m.Raw))
	}

	for _, m := range kg.kustomization.Spec.PatchesJSON6902 {
		patch, err := json.Marshal(m.Patch)
		if err != nil {
			return "", err
		}
		kus.PatchesJson6902 = append(kus.PatchesJson6902, kustypes.Patch{
			Patch:  string(patch),
			Target: adaptSelector(&m.Target),
		})
	}

	for _, image := range kg.kustomization.Spec.Images {
		newImage := kustypes.Image{
			Name:    image.Name,
			NewName: image.NewName,
			NewTag:  image.NewTag,
		}
		if exists, index := checkKustomizeImageExists(kus.Images, image.Name); exists {
			kus.Images[index] = newImage
		} else {
			kus.Images = append(kus.Images, newImage)
		}
	}

	kd, err := yaml.Marshal(kus)
	if err != nil {
		return "", err
	}

	return checksum, ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func checkKustomizeImageExists(images []kustypes.Image, imageName string) (bool, int) {
	for i, image := range images {
		if imageName == image.Name {
			return true, i
		}
	}

	return false, -1
}

func (kg *KustomizeGenerator) generateKustomization(dirPath string) error {
	fs := filesys.MakeFsOnDisk()

	// Determine if there already is a Kustomization file at the root,
	// as this means we do not have to generate one.
	for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
		if kpath := filepath.Join(dirPath, kfilename); fs.Exists(kpath) && !fs.IsDir(kpath) {
			return nil
		}
	}

	scan := func(base string) ([]string, error) {
		var paths []string
		uf := kunstruct.NewKunstructuredFactoryImpl()
		err := fs.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if path == base {
				return nil
			}
			if info.IsDir() {
				// If a sub-directory contains an existing kustomization file add the
				// directory as a resource and do not decend into it.
				for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
					if kpath := filepath.Join(path, kfilename); fs.Exists(kpath) && !fs.IsDir(kpath) {
						paths = append(paths, path)
						return filepath.SkipDir
					}
				}
				return nil
			}

			extension := filepath.Ext(path)
			if !containsString([]string{".yaml", ".yml"}, extension) {
				return nil
			}

			fContents, err := fs.ReadFile(path)
			if err != nil {
				return err
			}

			if _, err := uf.SliceFromBytes(fContents); err != nil {
				return fmt.Errorf("failed to decode Kubernetes YAML from %s: %w", path, err)
			}
			paths = append(paths, path)
			return nil
		})
		return paths, err
	}

	abs, err := filepath.Abs(dirPath)
	if err != nil {
		return err
	}

	files, err := scan(abs)
	if err != nil {
		return err
	}

	kfile := filepath.Join(dirPath, konfig.DefaultKustomizationFileName())
	f, err := fs.Create(kfile)
	if err != nil {
		return err
	}
	f.Close()

	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	var resources []string
	for _, file := range files {
		resources = append(resources, strings.Replace(file, abs, ".", 1))
	}

	kus.Resources = resources
	kd, err := yaml.Marshal(kus)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func (kg *KustomizeGenerator) checksum(ctx context.Context, dirPath string) (string, error) {
	if err := kg.generateKustomization(dirPath); err != nil {
		return "", fmt.Errorf("kustomize create failed: %w", err)
	}

	fs := filesys.MakeFsOnDisk()
	m, err := buildKustomization(fs, dirPath)
	if err != nil {
		return "", fmt.Errorf("kustomize build failed: %w", err)
	}

	// run variable substitutions
	if kg.kustomization.Spec.PostBuild != nil {
		for _, res := range m.Resources() {
			outRes, err := substituteVariables(ctx, kg.Client, kg.kustomization, res)
			if err != nil {
				return "", fmt.Errorf("var substitution failed for '%s': %w", res.GetName(), err)
			}

			if outRes != nil {
				_, err = m.Replace(res)
				if err != nil {
					return "", err
				}
			}
		}
	}

	resources, err := m.AsYaml()
	if err != nil {
		return "", fmt.Errorf("kustomize build failed: %w", err)
	}

	return fmt.Sprintf("%x", sha1.Sum(resources)), nil
}

func (kg *KustomizeGenerator) generateLabelTransformer(checksum, dirPath string) error {
	labels := selectorLabels(kg.kustomization.GetName(), kg.kustomization.GetNamespace())

	// add checksum label only if GC is enabled
	if kg.kustomization.Spec.Prune {
		labels = gcLabels(kg.kustomization.GetName(), kg.kustomization.GetNamespace(), checksum)
	}

	var lt = struct {
		ApiVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
		Metadata   struct {
			Name string `json:"name" yaml:"name"`
		} `json:"metadata" yaml:"metadata"`
		Labels     map[string]string    `json:"labels,omitempty" yaml:"labels,omitempty"`
		FieldSpecs []kustypes.FieldSpec `json:"fieldSpecs,omitempty" yaml:"fieldSpecs,omitempty"`
	}{
		ApiVersion: "builtin",
		Kind:       "LabelTransformer",
		Metadata: struct {
			Name string `json:"name" yaml:"name"`
		}{
			Name: kg.kustomization.GetName(),
		},
		Labels: labels,
		FieldSpecs: []kustypes.FieldSpec{
			{Path: "metadata/labels", CreateIfNotPresent: true},
		},
	}

	data, err := yaml.Marshal(lt)
	if err != nil {
		return err
	}

	labelsFile := filepath.Join(dirPath, transformerFileName)
	if err := ioutil.WriteFile(labelsFile, data, os.ModePerm); err != nil {
		return err
	}

	return nil
}

func adaptSelector(selector *kustomize.Selector) (output *kustypes.Selector) {
	if selector != nil {
		output = &kustypes.Selector{}
		output.Gvk.Group = selector.Group
		output.Gvk.Kind = selector.Kind
		output.Gvk.Version = selector.Version
		output.Name = selector.Name
		output.Namespace = selector.Namespace
		output.LabelSelector = selector.LabelSelector
		output.AnnotationSelector = selector.AnnotationSelector
	}
	return
}

// buildKustomization wraps krusty.MakeKustomizer with the following settings:
// - disable kyaml due to critical bugs like:
//	 - https://github.com/kubernetes-sigs/kustomize/issues/3446
//	 - https://github.com/kubernetes-sigs/kustomize/issues/3480
// - reorder the resources just before output (Namespaces and Cluster roles/role bindings first, CRDs before CRs, Webhooks last)
// - load files from outside the kustomization.yaml root
// - disable plugins except for the builtin ones
// - prohibit changes to resourceIds, patch name/kind don't overwrite target name/kind
func buildKustomization(fs filesys.FileSystem, dirPath string) (resmap.ResMap, error) {
	buildOptions := &krusty.Options{
		UseKyaml:               false,
		DoLegacyResourceSort:   true,
		LoadRestrictions:       kustypes.LoadRestrictionsNone,
		AddManagedbyLabel:      false,
		DoPrune:                false,
		PluginConfig:           konfig.DisabledPluginConfig(),
		AllowResourceIdChanges: false,
	}

	k := krusty.MakeKustomizer(fs, buildOptions)
	return k.Run(dirPath)
}
