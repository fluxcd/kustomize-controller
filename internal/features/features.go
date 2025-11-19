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

import (
	"github.com/fluxcd/pkg/auth"
	"github.com/fluxcd/pkg/runtime/controller"
	feathelper "github.com/fluxcd/pkg/runtime/features"
)

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

	// DisableFailFastBehavior controls whether the fail-fast behavior when
	// waiting for resources to become ready should be disabled.
	DisableFailFastBehavior = "DisableFailFastBehavior"

	// StrictPostBuildSubstitutions controls whether the post-build substitutions
	// should fail if a variable without a default value is declared in files
	// but is missing from the input vars.
	StrictPostBuildSubstitutions = "StrictPostBuildSubstitutions"

	// GroupChangeLog controls whether to group Kubernetes objects names in log output
	// to reduce cardinality of logs.
	GroupChangeLog = "GroupChangeLog"

	// AdditiveCELDependencyCheck controls whether the CEL dependency check
	// should be additive, meaning that the built-in readiness check will
	// be added to the user-defined CEL expressions.
	AdditiveCELDependencyCheck = "AdditiveCELDependencyCheck"

	// ExternalArtifact controls whether the ExternalArtifact source type is enabled.
	ExternalArtifact = "ExternalArtifact"

	// CancelHealthCheckOnNewRevision controls whether ongoing health checks
	// should be cancelled when a new source revision becomes available.
	//
	// When enabled, if a new revision is detected while waiting for resources
	// to become ready, the current health check will be cancelled to allow
	// immediate processing of the new revision. This can help avoid getting
	// stuck on failing deployments when fixes are available.
	CancelHealthCheckOnNewRevision = "CancelHealthCheckOnNewRevision"
)

var features = map[string]bool{
	// CacheSecretsAndConfigMaps
	// opt-in from v0.33
	CacheSecretsAndConfigMaps: false,
	// DisableStatusPollerCache
	// opt-out from v1.2
	DisableStatusPollerCache: true,
	// DisableFailFastBehavior
	// opt-in from v1.1
	DisableFailFastBehavior: false,
	// StrictPostBuildSubstitutions
	// opt-in from v1.3
	StrictPostBuildSubstitutions: false,
	// GroupChangeLog
	// opt-in from v1.5
	GroupChangeLog: false,
	// AdditiveCELDependencyCheck
	// opt-in from v1.7
	AdditiveCELDependencyCheck: false,
	// ExternalArtifact
	// opt-in from v1.7
	ExternalArtifact: false,
	// CancelHealthCheckOnNewRevision
	// opt-in from v1.7
	CancelHealthCheckOnNewRevision: false,
	// DisableConfigWatchers
	// opt-in from v1.7.3
	controller.FeatureGateDisableConfigWatchers: false,
}

func init() {
	auth.SetFeatureGates(features)
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
