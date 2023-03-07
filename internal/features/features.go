/*
Copyright 2023 The Flux authors

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

// Package features sets the feature gates that kustomize-controller supports,
// and their default states.
package features

import feathelper "github.com/fluxcd/pkg/runtime/features"

const (
	// CacheSecretsAndConfigMaps controls whether Secrets and ConfigMaps should
	// be cached.
	//
	// When enabled, it will cache both object types, resulting in increased
	// memory usage and cluster-wide RBAC permissions (list and watch).
	CacheSecretsAndConfigMaps = "CacheSecretsAndConfigMaps"

	// DisableStatusPollerCache controls whether the status polling cache
	// should be disabled.
	//
	// This may be useful when the controller is running in a cluster with a
	// large number of resources, as it will potentially reduce the amount of
	// memory used by the controller.
	DisableStatusPollerCache = "DisableStatusPollerCache"
)

var features = map[string]bool{
	// CacheSecretsAndConfigMaps
	// opt-in from v0.33
	CacheSecretsAndConfigMaps: false,
	// DisableStatusPollerCache
	// opt-in from v0.35
	DisableStatusPollerCache: false,
}

// FeatureGates contains a list of all supported feature gates and
// their default values.
func FeatureGates() map[string]bool {
	return features
}

// Enabled verifies whether the feature is enabled or not.
//
// This is only a wrapper around the Enabled func in
// pkg/runtime/features, so callers won't need to import both packages
// for checking whether a feature is enabled.
func Enabled(feature string) (bool, error) {
	return feathelper.Enabled(feature)
}

// Disable disables the specified feature. If the feature is not
// present, it's a no-op.
func Disable(feature string) {
	if _, ok := features[feature]; ok {
		features[feature] = false
	}
}
