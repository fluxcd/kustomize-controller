# Test for Revision Detection Bug

## The Bug
The kustomize-controller fails to reconcile new revisions after a health check failure due to incorrect revision detection logic in `kustomization_indexers.go:62`.

## Test Added
Added test case `reconciles new revision after health check failure` to `kustomization_wait_test.go` (lines 449-545).

## The Fix
In `internal/controller/kustomization_indexers.go:62`, change:
```diff
- if conditions.IsReady(&list.Items[i]) && repo.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
+ if conditions.IsReady(&list.Items[i]) && repo.GetArtifact().HasRevision(d.Status.LastAppliedRevision) {
```

## Test Scenario
1. Deploy a Kustomization with a bad image that fails health checks
2. Verify it becomes NOT Ready with LastAttemptedRevision = bad revision
3. Update GitRepository with fixed manifest (good image)
4. Verify Kustomization reconciles the new revision and becomes Ready

## Expected Behavior
- Test should FAIL with current code (Kustomization stays stuck on bad revision)
- Test should PASS after applying the fix (Kustomization reconciles new revision)

## To Run Test
```bash
# Install kubebuilder first if needed
make test

# Or run specific test once environment is set up
go test -v ./internal/controller -run "TestKustomizationReconciler_WaitConditions/reconciles_new_revision_after_health_check_failure"
```