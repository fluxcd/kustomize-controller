/*
Copyright 2026 The Flux authors

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

package v1

// Action describes an observable stage of the reconcile loop, from resolving
// the source and dependencies through applying, pruning and finalizing the
// desired state.
type Action string

// String returns the string representation of the Action.
func (a Action) String() string {
	return string(a)
}

const (
	// ActionReconcile denotes the overall outcome of the reconcile loop,
	// emitted once per run to report that reconciliation finished or failed.
	ActionReconcile Action = "Reconcile"

	// ActionCheckDependencies verifies that every Kustomization referenced by
	// spec.dependsOn is ready before reconciliation proceeds.
	ActionCheckDependencies Action = "CheckDependencies"

	// ActionResolveSource resolves and validates the source reference,
	// enforcing cross-namespace and ExternalArtifact access controls before
	// the artifact is fetched.
	ActionResolveSource Action = "ResolveSource"

	// ActionDecrypt decrypts SOPS-encrypted secrets inline before apply.
	ActionDecrypt Action = "Decrypt"

	// ActionApply reconciles the built manifests onto the cluster using
	// server-side apply, correcting any detected drift.
	ActionApply Action = "Apply"

	// ActionPrune garbage collects resources removed from source that remain
	// in the resource inventory.
	ActionPrune Action = "Prune"

	// ActionHealthCheck waits on the health of the applied resources using
	// kstatus and any configured health check expressions.
	ActionHealthCheck Action = "HealthCheck"

	// ActionFinalize prunes managed resources when the Kustomization is
	// deleted, according to its deletion policy.
	ActionFinalize Action = "Finalize"

	// ActionWaitForTermination blocks until resources marked for deletion are
	// removed by the Kubernetes garbage collector, when the deletion policy
	// requires it.
	ActionWaitForTermination Action = "WaitForTermination"
)
