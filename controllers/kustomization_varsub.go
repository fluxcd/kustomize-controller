package controllers

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/drone/envsubst"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/resource"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"
)

// varsubRegex is the regular expression used to validate
// the var names before substitution
const varsubRegex = "^[_[:alpha:]][_[:alpha:][:digit:]]*$"

// substitudeFrom loads vars from configmaps and secrets referenced
// in the given SubstituteReference
func substitudeFrom(
	ctx context.Context,
	kubeClient client.Client,
	kustomization kustomizev1.Kustomization,
	substituteReference []kustomizev1.SubstituteReference) (map[string]string, error) {
	// placeholder variable for returnable substitutions
	vars := make(map[string]string)

	// load vars from ConfigMaps and Secrets data keys
	for _, reference := range substituteReference {
		namespacedName := types.NamespacedName{Namespace: kustomization.Namespace, Name: reference.Name}
		switch reference.Kind {
		case "ConfigMap":
			resource := &corev1.ConfigMap{}
			if err := kubeClient.Get(ctx, namespacedName, resource); err != nil {
				return nil, fmt.Errorf("substitute from 'ConfigMap/%s' error: %w", reference.Name, err)
			}
			for k, v := range resource.Data {
				vars[k] = strings.Replace(v, "\n", "", -1)
			}
		case "Secret":
			resource := &corev1.Secret{}
			if err := kubeClient.Get(ctx, namespacedName, resource); err != nil {
				return nil, fmt.Errorf("substitute from 'Secret/%s' error: %w", reference.Name, err)
			}
			for k, v := range resource.Data {
				vars[k] = strings.Replace(string(v), "\n", "", -1)
			}
		}
	}
	return vars, nil
}

// substituteVariables replaces the vars with their values in the specified resource.
// If a resource is labeled or annotated with
func preSubstituteVariables(
	ctx context.Context,
	kubeClient client.Client,
	kustomization kustomizev1.Kustomization,
	kustomizeFile kustypes.Kustomization) (kustypes.Kustomization, error) {
	kd := kustypes.Kustomization{}
	kustomizeData, err := yaml.Marshal(kustomizeFile)
	if err != nil {
		return kd, err
	}

	// load vars from ConfigMaps and Secrets data keys
	vars, err := substitudeFrom(ctx, kubeClient, kustomization, kustomization.Spec.PreBuild.SubstituteFrom)
	if err != nil {
		return kd, err
	}

	// load in-line vars (overrides the ones from resources)
	if kustomization.Spec.PreBuild.Substitute != nil {
		for k, v := range kustomization.Spec.PreBuild.Substitute {
			vars[k] = strings.Replace(v, "\n", "", -1)
		}
	}

	// run bash variable substitutions
	r, _ := regexp.Compile(varsubRegex)
	for v := range vars {
		if !r.MatchString(v) {
			return kd, fmt.Errorf("'%s' var name is invalid, must match '%s'", v, varsubRegex)
		}
	}

	output, err := envsubst.Eval(string(kustomizeData), func(s string) string {
		return vars[s]
	})
	if err != nil {
		return kd, fmt.Errorf("variable substitution failed: %w", err)
	}

	json, err := yaml.YAMLToJSON([]byte(output))
	if err != nil {
		return kd, fmt.Errorf("Prebuild variable substitution failed: %w", err)
	}

	err = yaml.Unmarshal(json, &kd)
	if err != nil {
		return kd, err
	}

	return kd, nil
}

// substituteVariables replaces the vars with their values in the specified resource.
// If a resource is labeled or annotated with
// 'kustomize.toolkit.fluxcd.io/substitute: disabled' the substitution is skipped.
func substituteVariables(
	ctx context.Context,
	kubeClient client.Client,
	kustomization kustomizev1.Kustomization,
	res *resource.Resource) (*resource.Resource, error) {
	resData, err := res.AsYAML()
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s/substitute", kustomizev1.GroupVersion.Group)

	if res.GetLabels()[key] == kustomizev1.DisabledValue || res.GetAnnotations()[key] == kustomizev1.DisabledValue {
		return nil, nil
	}

	// load vars from ConfigMaps and Secrets data keys
	vars, err := substitudeFrom(ctx, kubeClient, kustomization, kustomization.Spec.PostBuild.SubstituteFrom)
	if err != nil {
		return nil, err
	}

	// load in-line vars (overrides the ones from resources)
	if kustomization.Spec.PostBuild.Substitute != nil {
		for k, v := range kustomization.Spec.PostBuild.Substitute {
			vars[k] = strings.Replace(v, "\n", "", -1)
		}
	}

	// run bash variable substitutions
	if len(vars) > 0 {
		r, _ := regexp.Compile(varsubRegex)
		for v := range vars {
			if !r.MatchString(v) {
				return nil, fmt.Errorf("'%s' var name is invalid, must match '%s'", v, varsubRegex)
			}
		}

		output, err := envsubst.Eval(string(resData), func(s string) string {
			return vars[s]
		})
		if err != nil {
			return nil, fmt.Errorf("variable substitution failed: %w", err)
		}

		jsonData, err := yaml.YAMLToJSON([]byte(output))
		if err != nil {
			return nil, fmt.Errorf("YAMLToJSON: %w", err)
		}

		err = res.UnmarshalJSON(jsonData)
		if err != nil {
			return nil, fmt.Errorf("UnmarshalJSON: %w", err)
		}
	}

	return res, nil
}
