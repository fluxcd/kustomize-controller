# AGENTS.md

Guidance for AI coding assistants working in `fluxcd/kustomize-controller`. Read this file before making changes.

## Contribution workflow for AI agents

These rules come from [`fluxcd/flux2/CONTRIBUTING.md`](https://github.com/fluxcd/flux2/blob/main/CONTRIBUTING.md) and apply to every Flux repository.

- **Do not add `Signed-off-by` or `Co-authored-by` trailers with your agent name.** Only a human can legally certify the DCO.
- **Disclose AI assistance** with an `Assisted-by` trailer naming your agent and model:
  ```sh
  git commit -s -m "Add support for X" --trailer "Assisted-by: <agent-name>/<model-id>"
  ```
  The `-s` flag adds the human's `Signed-off-by` from their git config — do not remove it.
- **Commit message format:** Subject in imperative mood ("Add feature X" instead of "Adding feature X"), capitalized, no trailing period, ≤50 characters. Body wrapped at 72 columns, explaining what and why. No `@mentions` or `#123` issue references in the commit — put those in the PR description.
- **Trim verbiage:** in PR descriptions, commit messages, and code comments. No marketing prose, no restating the diff, no emojis.
- **Rebase, don't merge:** Never merge `main` into the feature branch; rebase onto the latest `main` and push with `--force-with-lease`. Squash before merge when asked.
- **Pre-PR gate:** `make tidy fmt vet && make test` must pass and the working tree must be clean after codegen. Commit regenerated files in the same PR.
- **Flux is GA:** Backward compatibility is mandatory. Breaking changes to CRD fields, status, CLI flags, metrics, or observable behavior will be rejected. Design additive changes and keep older API versions round-tripping.
- **Copyright:** All new `.go` files must begin with the boilerplate from `hack/boilerplate.go.txt` (Apache 2.0). Update the year to the current year when copying.
- **Spec docs:** New features and API changes must be documented in `docs/spec/v1/kustomizations.md`. Update it in the same PR that introduces the change.
- **Tests:** New features, improvements and fixes must have test coverage. Add unit tests in `internal/controller/*_test.go` and other `internal/*` packages as appropriate. Follow the existing patterns for test organization, fixtures, and assertions. Run tests locally before pushing.

## Code quality

Before submitting code, review your changes for the following:

- **No secrets in logs or events.** Never surface decryption keys, auth tokens, passwords, or credential URLs in error messages, conditions, events, or log lines. SOPS key material (PGP, age, KMS credentials) must never be logged or returned in errors.
- **No unchecked I/O.** Close HTTP response bodies, file handles, and archive readers in `defer` statements. Check and propagate errors from `io.Copy`, `os.Remove`, `os.Rename`.
- **No path traversal.** Validate and sanitize file paths extracted from source artifacts. Kustomize builds must stay within the source root; respect `--no-remote-bases` to block remote base fetches.
- **No unbounded reads.** Use `io.LimitReader` when reading from network or archive sources. Respect existing size limits; do not introduce new reads without bounds.
- **No command injection.** Do not shell out via `os/exec`. Kustomize builds go through `fluxcd/pkg/kustomize`, not the `kustomize` CLI.
- **No hardcoded defaults for security settings.** TLS verification must remain enabled by default; impersonation and cross-namespace ref restrictions must be honored.
- **SSA field manager integrity.** Always use the controller's field manager via `fluxcd/pkg/ssa`. Never hard-code a different manager or strip fields out of band — changing it orphans fields on every managed object.
- **Error handling.** Wrap errors with `%w` for chain inspection. Do not swallow errors silently. Return actionable error messages that help users diagnose the issue without leaking internal state.
- **Resource cleanup.** Ensure temporary files, directories, and fetched artifacts are cleaned up on all code paths (success and error). Use `defer` and `t.TempDir()` in tests.
- **Concurrency safety.** Do not introduce shared mutable state without synchronization. Reconcilers run concurrently; per-object work must be isolated.
- **No panics.** Never use `panic` in runtime code paths. Return errors and let the reconciler handle them gracefully.
- **Minimal surface.** Keep new exported APIs, flags, and environment variables to the minimum needed. Every export is a backward-compatibility commitment.

## Project overview

kustomize-controller is a core component of the [Flux GitOps Toolkit](https://fluxcd.io/flux/components/). It reconciles the `Kustomization` CRD (`kustomize.toolkit.fluxcd.io`). For each object it:

1. Fetches a source artifact produced by source-controller from a `GitRepository`, `Bucket`, `OCIRepository`, or (feature-gated) generic `ExternalArtifact`.
2. Runs a Kustomize build against the referenced path, with optional `postBuild` variable substitution (envsubst).
3. Decrypts SOPS-encrypted Kubernetes secrets inline using keys from PGP, age, Hashicorp Vault, AWS KMS, GCP KMS, or Azure Key Vault.
4. Applies the resulting manifests with server-side apply using a stable field manager, detects drift, prunes objects removed from source via a resource inventory, and waits on health checks using `cli-utils` kstatus.
5. Optionally impersonates a `ServiceAccount` and/or targets a remote cluster via a `kubeconfig` secret.

It depends on source-controller only at runtime for artifact delivery; it never talks to Git or OCI directly.

## Repository layout

- `main.go` — controller entrypoint: flag parsing, feature gate wiring, manager setup, `KustomizationReconciler` construction.
- `api/` — separate Go module (`github.com/fluxcd/kustomize-controller/api`) with CRD types. Imported via `replace` from the root module. Contains `v1` (storage), `v1beta1`, `v1beta2`.
  - `api/v1/` — `kustomization_types.go`, `inventory_types.go`, `reference_types.go`, generated `zz_generated.deepcopy.go`.
- `internal/controller/` — `kustomization_controller.go` (reconcile loop), `kustomization_manager.go` (SetupWithManager / watches), `kustomization_indexers.go`, `source_predicate.go`, `constants.go`, `utils.go`, `suite_test.go`, per-feature `*_test.go`. `testdata/` holds manifest fixtures.
- `internal/decryptor/` — SOPS integration. Resolves PGP, age, Vault token, AWS/GCP/Azure KMS credentials (including workload identity) and decrypts secrets inside the Kustomize resource tree.
- `internal/inventory/` — translates `ssa.ChangeSet` results into `ResourceInventory` on `Kustomization.status` and back for pruning/diffing.
- `internal/sops/awskms/`, `internal/sops/azkv/`, `internal/sops/keyservice/` — provider-specific SOPS plumbing plus an in-process key service used by the decryptor to avoid shelling out to `sops`.
- `internal/features/` — feature gate definitions and defaults (`DisableStatusPollerCache`, `StrictPostBuildSubstitutions`, `CancelHealthCheckOnNewRevision`, etc.).
- `config/` — Kustomize overlays. `config/crd/bases/` holds the generated controller CRD plus source-controller CRDs downloaded for envtest. `config/manager/`, `config/rbac/`, `config/default/`, `config/samples/`, `config/testdata/`.
- `hack/` — `boilerplate.go.txt` license header and `api-docs/` templates.
- `docs/` — `spec/v1/` user-facing API spec, `api/v1/` generated reference, `diagrams/`, `internal/` notes.

## APIs and CRDs

- Group: `kustomize.toolkit.fluxcd.io`. Kind: `Kustomization`.
- Storage/served version: `v1`. `v1beta1` and `v1beta2` remain for conversion compatibility — do not remove them.
- Types in `api/<version>/kustomization_types.go`, `reference_types.go`, `inventory_types.go`. Deep-copy methods are generated into `zz_generated.deepcopy.go` — never hand-edit.
- `config/crd/bases/kustomize.toolkit.fluxcd.io_kustomizations.yaml` is generated by `make manifests`. Other files in that directory (`gitrepositories.yaml`, `buckets.yaml`, `ocirepositories.yaml`, `externalartifacts.yaml`) are **downloaded** from the pinned source-controller version by `make download-crd-deps` — do not edit them.
- `api/` is its own Go module. When you change types, run `make generate manifests api-docs` from the repo root; those targets drive `controller-gen` inside `api/` via the `replace` directive.

## Build, test, lint

All targets live in the root `Makefile`. Tool binaries land in `build/gobin/`.

- `make tidy` — tidy both the root and `api/` modules.
- `make fmt` / `make vet` — run in both modules.
- `make generate` — `controller-gen object` against `api/` (deepcopy).
- `make manifests` — regenerate CRDs and RBAC under `config/crd/bases` from both modules.
- `make api-docs` — regenerate `docs/api/v1/kustomize.md`.
- `make manager` — build the binary to `build/bin/manager`.
- `make run` — run the controller locally. Requires `SOURCE_CONTROLLER_LOCALHOST` if source-controller is port-forwarded (see `DEVELOPMENT.md`).
- `make test` — canonical test target. Chains `tidy generate fmt vet manifests api-docs download-crd-deps install-envtest` and a local `sops` binary, then runs `go test ./... -v -coverprofile cover.out`. Honors `GO_TEST_ARGS`, `ENVTEST_KUBERNETES_VERSION`, `ENVTEST_ARCH`.
- `make install-envtest` — download envtest binaries via `setup-envtest`.
- `make install` / `make deploy` / `make dev-deploy` / `make docker-build` / `make docker-push` — cluster workflows.
- `make verify` — runs `fmt` and fails if the working tree is dirty.

## Codegen and generated files

Check `go.mod` and the `Makefile` for current dependency and tool versions. After changing API types or kubebuilder markers, regenerate and commit the results:

```sh
make generate manifests api-docs
```

Generated files (never hand-edit):

- `api/*/zz_generated.deepcopy.go`
- `config/crd/bases/kustomize.toolkit.fluxcd.io_kustomizations.yaml`
- `config/crd/bases/{gitrepositories,buckets,ocirepositories,externalartifacts}.yaml` (downloaded from source-controller)
- `docs/api/v1/kustomize.md`

Load-bearing `replace` directives in `go.mod` — do not remove:

- `sigs.k8s.io/kustomize/api` + `kyaml` pinned via `replace`. Do not bump without coordinating with maintainers; kustomize upgrades are breaking.
- `opencontainers/go-digest` pinned to a master snapshot for BLAKE3 support.

The `fluxcd/source-controller/api` version in `go.mod` drives `SOURCE_VER` in the Makefile and determines which source CRDs are downloaded for envtest. Bumping it requires re-running `make download-crd-deps` (or deleting `build/.src-crd-*`).

Bump `fluxcd/pkg/*` modules as a set — version skew breaks `go.sum`. Run `make tidy` after any bump.

## Conventions

- Standard `gofmt`. All exported types, functions, and package declarations need doc comments; non-trivial unexported declarations too. Error messages lowercase, wrapped with `%w`.
- **Kustomize builds** go through `github.com/fluxcd/pkg/kustomize` (imported as `generator`). Respect `--no-remote-bases` via `KustomizationReconciler.NoRemoteBases`.
- **Server-side apply** goes through `github.com/fluxcd/pkg/ssa`. Two field managers are used: `gotk-kustomize-controller` for status and finalizers (set as `StatusManager` in `main.go`), and `kustomize-controller` for managed objects applied via SSA.
- **Inventory and pruning.** Owned objects are persisted on `Kustomization.status.inventory` as `ResourceRef{ID, Version}` where `ID` is a `cli-utils` `ObjMetadata` string. Helpers live in `internal/inventory`. Pruning diffs the previous inventory against the new change set; corrupting the format breaks upgrades.
- **Drift detection / correction.** SSA reapply with force corrects drift. Resources marked `ignore` or without the diff filter must be skipped — reuse the existing helpers.
- **Impersonation.** If `spec.serviceAccountName` is set (or `--default-service-account` is configured), the reconciler builds a scoped REST config via `fluxcd/pkg/runtime/client` and `fluxcd/pkg/auth`. When `--no-cross-namespace-refs` is set, cross-namespace references are rejected — honor `NoCrossNamespaceRefs` in any new ref-resolving code.
- **Multi-tenancy lockdown flags:** `--default-service-account`, `--default-decryption-service-account`, `--default-kubeconfig-service-account`, `--no-remote-bases`, `--no-cross-namespace-refs`, and the `ObjectLevelWorkloadIdentity` feature gate. Don't invent parallel mechanisms.
- **Field-manager override.** `--override-manager` configures `DisallowedFieldManagers`; conflicting managers get their fields reclaimed.
- **Feature gates.** Add new gates to `internal/features/features.go` with a default and a doc comment. Check them in `main.go` during setup and propagate booleans into `KustomizationReconciler` fields — do not call `features.Enabled` from hot reconcile paths.
- **Post-build substitution** (`spec.postBuild.substitute` / `substituteFrom`) runs envsubst after kustomize build. `StrictPostBuildSubstitutions` controls whether a missing variable without a default is an error.
- **Dependency ordering.** `spec.dependsOn` requires referenced Kustomizations to be `Ready` (and with `AdditiveCELDependencyCheck`, to satisfy a CEL expression) before reconciling. Respect `requeueDependency`; never bypass the check.

## Testing

- Primary tests run with envtest. `make test` downloads envtest binaries via `setup-envtest` into `build/testbin/` and exports `KUBEBUILDER_ASSETS`.
- Controller test suite lives in `internal/controller/`. `suite_test.go` wires up a `fluxcd/pkg/runtime/testenv.Environment`, registers the reconciler, starts an in-process `fluxcd/pkg/testserver` artifact server, and (for SOPS/Vault tests) a Vault container via `ory/dockertest`. Tests use Gomega.
- Fixtures: `internal/controller/testdata/`, `internal/decryptor/testdata/`, `internal/inventory/testdata/`, `config/testdata/`.
- Run a single test: `make test GO_TEST_ARGS='-run TestKustomizationReconciler_Foo'`.
- Vault and SOPS tests spin up Docker containers — a working Docker daemon is required.
- `DEBUG_TEST=1` enables verbose zap logging inside `suite_test.go`.

## Gotchas and non-obvious rules

- `api/` is a **separate Go module** with its own `go.mod`. Forgetting to tidy it (done by `make tidy`) breaks downstream consumers.
- `go.mod` pins `sigs.k8s.io/kustomize/api` and `kyaml` via `replace`. Do not remove the replace or bump these independently.
- `source-controller` CRDs under `config/crd/bases/` are downloaded and version-tracked via `build/.src-crd-<version>`. If you change `fluxcd/source-controller/api` in `go.mod`, delete that marker (or run `make cleanup-crd-deps`) so the next `make test` pulls fresh CRDs.
- SOPS age keys for fallback decryption come from the secret named by `--sops-age-secret` in the controller's runtime namespace, not from the `Kustomization`'s namespace. Per-`Kustomization` keys come from `spec.decryption.secretRef`.
- The managed-object field manager name (`kustomize-controller`) is load-bearing for drift detection across restarts. Similarly, `gotk-kustomize-controller` owns status and finalizer fields. See Code quality for consequences of changing either.
- `ResourceInventory` format must stay backward-compatible: `ID` is a `cli-utils` `ObjMetadata` string and `Version` is the apiVersion at apply time. Do not reorder or re-encode.
- Post-build substitution uses envsubst semantics, not Go templates. `$VAR` and `${VAR}` are substituted; escape with `$$`.
- `dependsOn` is evaluated every reconcile; a Kustomization that depends on itself transitively wedges the requeue behavior.
- `fluxcd/pkg` version skew: controllers in the Flux distribution expect compatible `pkg/runtime`, `pkg/ssa`, and `pkg/kustomize`. When bumping one, check the others and run the full `make test`.
- `main.go` registers only `kustomizev1` and `sourcev1` with the scheme. A new watched type means updating `init()` **and** the `SetupWithManager` watches in `internal/controller/kustomization_manager.go`, and honoring `--watch-label-selector` via `mgrConfig.Cache.ByObject`.
