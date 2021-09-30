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
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/provider"
	"sigs.k8s.io/kustomize/api/resmap"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/kustomize"
)

const (
	transformerFileName           = "kustomization-gc-labels.yaml"
	transformerAnnotationFileName = "kustomization-gc-annotations.yaml"
)

type KustomizeGenerator struct {
	kustomization kustomizev1.Kustomization
	client.Client
}

func NewGenerator(kustomization kustomizev1.Kustomization, kubeClient client.Client) *KustomizeGenerator {
	return &KustomizeGenerator{
		kustomization: kustomization,
		Client:        kubeClient,
	}
}

func (kg *KustomizeGenerator) WriteFile(ctx context.Context, dirPath string, kubeClient client.Client, kustomization kustomizev1.Kustomization) (string, error) {
	kfile := filepath.Join(dirPath, konfig.DefaultKustomizationFileName())

	checksum, err := kg.checksum(ctx, dirPath)
	if err != nil {
		return "", err
	}

	if err := kg.generateLabelTransformer(checksum, dirPath); err != nil {
		return "", err
	}
	if err = kg.generateAnnotationTransformer(checksum, dirPath); err != nil {
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
		kus.Transformers = []string{transformerFileName, transformerAnnotationFileName}
	} else {
		if !find(kus.Transformers, transformerFileName) {
			kus.Transformers = append(kus.Transformers, transformerFileName)
		}

		if !find(kus.Transformers, transformerAnnotationFileName) {
			kus.Transformers = append(kus.Transformers, transformerAnnotationFileName)
		}

	}

	if kg.kustomization.Spec.TargetNamespace != "" {
		kus.Namespace = kg.kustomization.Spec.TargetNamespace
	}

	for _, m := range kg.kustomization.Spec.Patches {
		kus.Patches = append(kus.Patches, kustypes.Patch{
			Patch:  m.Patch,
			Target: adaptSelector(&m.Target),
		})
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

	// if prebuild is specified, substitude kustomization with given variables
	// and return resulting kustomization
	if kustomization.Spec.PreBuild != nil {
		kus, err = preSubstituteVariables(ctx, kubeClient, kustomization, kus)
		if err != nil {
			return "", fmt.Errorf("var substitution failed for '%s'", err)
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

func (kg *KustomizeGenerator) generateKustomization(dirPath string) (string, error) {
	fs := filesys.MakeFsOnDisk()

	// Determine if there already is a Kustomization file at the root,
	// as this means we do not have to generate one.
	for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
		if kpath := filepath.Join(dirPath, kfilename); fs.Exists(kpath) && !fs.IsDir(kpath) {
			return kpath, nil
		}
	}

	scan := func(base string) ([]string, error) {
		var paths []string
		pvd := provider.NewDefaultDepProvider()
		rf := pvd.GetResourceFactory()
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

			if _, err := rf.SliceFromBytes(fContents); err != nil {
				return fmt.Errorf("failed to decode Kubernetes YAML from %s: %w", path, err)
			}
			paths = append(paths, path)
			return nil
		})
		return paths, err
	}

	abs, err := filepath.Abs(dirPath)
	if err != nil {
		return "", err
	}

	files, err := scan(abs)
	if err != nil {
		return "", err
	}

	kfile := filepath.Join(dirPath, konfig.DefaultKustomizationFileName())
	f, err := fs.Create(kfile)
	if err != nil {
		return "", err
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
		return "", err
	}

	return kfile, ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func (kg *KustomizeGenerator) checksum(ctx context.Context, dirPath string) (string, error) {
	kf, err := kg.generateKustomization(dirPath)
	if err != nil {
		return "", fmt.Errorf("kustomize create failed: %w", err)
	}

	// Run PreBuild Variable Substitution
	if kg.kustomization.Spec.PreBuild != nil {
		content, err := ioutil.ReadFile(kf)
		if err != nil {
			return "", fmt.Errorf("kustomize prebuild failed: %w", err)
		}

		kd := kustypes.Kustomization{}
		err = yaml.Unmarshal(content, &kd)
		if err != nil {
			return "", fmt.Errorf("kustomize prebuild failed: %w", err)
		}

		kd, err = preSubstituteVariables(ctx, kg.Client, kg.kustomization, kd)
		if err != nil {
			return "", fmt.Errorf("var substitution failed for '%s'", err)
		}

		kdYaml, err := yaml.Marshal(kd)
		ioutil.WriteFile(kf, kdYaml, os.ModePerm)
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

func (kg *KustomizeGenerator) generateAnnotationTransformer(checksum, dirPath string) error {
	var annotations map[string]string
	// add checksum annotations only if GC is enabled
	if kg.kustomization.Spec.Prune {
		annotations = gcAnnotation(checksum)
	}

	var lt = struct {
		ApiVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
		Metadata   struct {
			Name string `json:"name" yaml:"name"`
		} `json:"metadata" yaml:"metadata"`
		Annotations map[string]string    `json:"annotations,omitempty" yaml:"annotations,omitempty"`
		FieldSpecs  []kustypes.FieldSpec `json:"fieldSpecs,omitempty" yaml:"fieldSpecs,omitempty"`
	}{
		ApiVersion: "builtin",
		Kind:       "AnnotationsTransformer",
		Metadata: struct {
			Name string `json:"name" yaml:"name"`
		}{
			Name: kg.kustomization.GetName(),
		},
		Annotations: annotations,
		FieldSpecs: []kustypes.FieldSpec{
			{Path: "metadata/annotations", CreateIfNotPresent: true},
		},
	}

	data, err := yaml.Marshal(lt)
	if err != nil {
		return err
	}

	annotationsFile := filepath.Join(dirPath, transformerAnnotationFileName)
	if err := ioutil.WriteFile(annotationsFile, data, os.ModePerm); err != nil {
		return err
	}

	return nil
}

func (kg *KustomizeGenerator) generateLabelTransformer(checksum, dirPath string) error {
	labels := selectorLabels(kg.kustomization.GetName(), kg.kustomization.GetNamespace())

	labels = gcLabels(kg.kustomization.GetName(), kg.kustomization.GetNamespace(), checksum)

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

// TODO: remove mutex when kustomize fixes the concurrent map read/write panic
var kustomizeBuildMutex sync.Mutex

// buildKustomization wraps krusty.MakeKustomizer with the following settings:
// - reorder the resources just before output (Namespaces and Cluster roles/role bindings first, CRDs before CRs, Webhooks last)
// - load files from outside the kustomization.yaml root
// - disable plugins except for the builtin ones
func buildKustomization(fs filesys.FileSystem, dirPath string) (resmap.ResMap, error) {
	// temporary workaround for concurrent map read and map write bug
	// https://github.com/kubernetes-sigs/kustomize/issues/3659
	kustomizeBuildMutex.Lock()
	defer kustomizeBuildMutex.Unlock()

	buildOptions := &krusty.Options{
		DoLegacyResourceSort: true,
		LoadRestrictions:     kustypes.LoadRestrictionsNone,
		AddManagedbyLabel:    false,
		DoPrune:              false,
		PluginConfig:         kustypes.DisabledPluginConfig(),
	}

	k := krusty.MakeKustomizer(buildOptions)
	return k.Run(fs, dirPath)
}

func find(source []string, value string) bool {
	for _, item := range source {
		if item == value {
			return true
		}
	}

	return false
}
