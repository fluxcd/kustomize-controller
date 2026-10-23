# Kustomize Controller

## Flags

| Name                                   | Type          | Description                                                                                                                                                                                                                                         |
|----------------------------------------|---------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--authorized-artifact-server-spiffe-id` | strings     | SPIFFE ID of an artifact server authorized to serve artifacts to kustomize-controller. Can be repeated. When set, the artifact fetcher uses SPIFFE mTLS.                                                                                              |
| `--authorized-event-server-spiffe-id` | strings      | SPIFFE ID of an event server authorized to receive events from kustomize-controller. Can be repeated. When set, the event recorder uses SPIFFE mTLS.                                                                                                  |
| `--concurrent`                         | int           | The number of concurrent kustomize reconciles. (default 4)                                                                                                                                                                                          |
| `--concurrent-ssa`                     | int           | The number of concurrent server-side apply operations. (default 4)                                                                                                                                                                                  |
| `--custom-apply-stage-kinds`           | string        | A comma-separated list of GroupKind (e.g., 'rbac.authorization.k8s.io/Role,some.group.io/SomeResource') resources to be applied in a custom stage during server-side apply running after CRDs and before all namespaced resources not in this list. |
| `--default-decryption-service-account` | string        | Default service account used for decryption.                                                                                                                                                                                                        |
| `--default-kubeconfig-service-account` | string        | Default service account used for kubeconfig.                                                                                                                                                                                                        |
| `--default-service-account`            | string        | Default service account used for impersonation.                                                                                                                                                                                                     |
| `--enable-leader-election`             | boolean       | Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.                                                                                                                               |
| `--events-addr`                        | string        | The address of the events receiver.                                                                                                                                                                                                                 |
| `--health-addr`                        | string        | The address the health endpoint binds to. (default ":9440")                                                                                                                                                                                         |
| `--http-retry`                         | int           | The maximum number of retries when failing to fetch artifacts over HTTP. (default 9)                                                                                                                                                                |
| `--insecure-kubeconfig-exec`           | boolean       | Allow use of the user.exec section in kubeconfigs provided for remote apply.                                                                                                                                                                        |
| `--insecure-kubeconfig-tls`            | boolean       | Allow that kubeconfigs provided for remote apply can disable TLS verification.                                                                                                                                                                      |
| `--interval-jitter-percentage`         | uint8         | Percentage of jitter to apply to interval durations. A value of 10 will apply a jitter of +/-10% to the interval duration. It cannot be negative, and must be less than 100. (default 5)                                                            |
| `--leader-election-lease-duration`     | duration      | Interval at which non-leader candidates will wait to force acquire leadership (duration string). (default 35s)                                                                                                                                      |
| `--leader-election-release-on-cancel`  | boolean       | Defines if the leader should step down voluntarily on controller manager shutdown. (default true)                                                                                                                                                   |
| `--leader-election-renew-deadline`     | duration      | Duration that the leading controller manager will retry refreshing leadership before giving up (duration string). (default 30s)                                                                                                                     |
| `--leader-election-retry-period`       | duration      | Duration the LeaderElector clients should wait between tries of actions (duration string). (default 5s)                                                                                                                                             |
| `--log-encoding`                       | string        | Log encoding format. Can be 'json' or 'console'. (default "json")                                                                                                                                                                                   |
| `--log-level`                          | string        | Log verbosity level. Can be one of 'trace', 'debug', 'info', 'error'. (default "info")                                                                                                                                                              |
| `--max-retry-delay`                    | duration      | The maximum amount of time for which an object being reconciled will have to wait before a retry. (default 15m0s)                                                                                                                                   |
| `--metrics-addr`                       | string        | The address the metric endpoint binds to. (default ":8080")                                                                                                                                                                                         |
| `--min-retry-delay`                    | duration      | The minimum amount of time for which an object being reconciled will have to wait before a retry. (default 750ms)                                                                                                                                   |
| `--no-cross-namespace-refs`            | boolean       | When set to true, references between custom resources are allowed only if the reference and the referee are in the same namespace.                                                                                                                  |
| `--no-remote-bases`                    | boolean       | Disallow remote bases usage in Kustomize overlays. When this flag is enabled, all resources must refer to local files included in the source artifact.                                                                                              |
| `--override-manager`                   | stringArray   | Field manager disallowed to perform changes on managed resources.                                                                                                                                                                                   |
| `--override-manager-before-dry-run`    | stringArray   | Field manager taken over before the server-side apply dry-run. Use only to recover objects that fail the dry-run due to stale managers, see [below](#override-managers-before-dry-run). |
| `--requeue-dependency`                 | duration      | The interval at which failing dependencies are reevaluated. (default 30s)                                                                                                                                                                           |
| `--sops-age-secret`                    | string        | The name of a Kubernetes secret in the RUNTIME_NAMESPACE containing a SOPS age decryption key for fallback usage.                                                                                                                                   |
| `--sops-vault-configmap`               | string        | The name of a Kubernetes ConfigMap in the RUNTIME_NAMESPACE containing an OpenBao/Vault configuration with instances and login paths for SOPS decryption.                                                                                           |
| `--token-cache-max-size`               | int           | The maximum amount of entries in the LRU cache used for tokens. (default 100, enabled)                                                                                                                                                              |
| `--spiffe-broker-endpoint`            | string        | The gRPC URL of the SPIFFE Broker endpoint.                                                                                                                                              |
| `--spiffe-broker-id`                  | strings       | SPIFFE ID of the SPIFFE Broker endpoint. Can be repeated.                                                                                                                                |
| `--token-cache-max-duration`           | duration      | The maximum duration for which a token would be considered unexpired. This is capped at 1h. (default 1h)                                                                                                                                            |
| `--watch-all-namespaces`               | boolean       | Watch for custom resources in all namespaces, if set to false it will only watch the runtime namespace. (default true)                                                                                                                              |
| `--watch-configs-label-selector`       | string        | Watch for ConfigMaps and Secrets with matching labels (default 'reconcile.fluxcd.io/watch=Enabled').                                                                                                                                                |
| `--watch-label-selector`               | string        | Watch for resources with matching labels e.g. 'sharding.fluxcd.io/key=shard1'.                                                                                                                                                                      |
| `--feature-gates`                      | mapStringBool | A comma separated list of key=value pairs defining the state of experimental features.                                                                                                                                                              |

### SPIFFE mTLS

When at least one of the repeatable flags `--authorized-artifact-server-spiffe-id`
or `--authorized-event-server-spiffe-id` is set, kustomize-controller acquires an
X.509-SVID from the SPIFFE Workload API. The `SPIFFE_ENDPOINT_SOCKET` environment
variable must point to the Workload API socket.

- `--authorized-artifact-server-spiffe-id` authorizes the given SPIFFE IDs of
  artifact servers from which kustomize-controller fetches artifacts. When set,
  the artifact fetcher uses mTLS.
- `--authorized-event-server-spiffe-id` authorizes the given SPIFFE IDs of
  event servers to which kustomize-controller sends events. When set, the event
  recorder uses mTLS.

Each traffic type is protected independently. SPIFFE mTLS is transparent to the
Flux API: the scheme of `.status.artifact.url` does not change.

Each flag value is interpreted as follows:

- `spiffe://<trust domain>/<path>` authorizes exactly that SPIFFE ID.
- `spiffe://<trust domain>/<path>/` (with a trailing slash) authorizes any
  SPIFFE ID with that path prefix.
- `spiffe://<trust domain>` (without a path) authorizes any SPIFFE ID in that
  trust domain.

Whenever `<trust domain>` equals the special value `self`, kustomize-controller
understands it as the trust domain of the controller's own X509-SVID.

Invalid values cause kustomize-controller to fail at startup.

### SPIFFE Broker API

When `--spiffe-broker-endpoint` and at least one `--spiffe-broker-id` are set,
kustomize-controller acquires an X.509-SVID from the SPIFFE Workload API and uses it to
authenticate to the SPIFFE Broker API. The `SPIFFE_ENDPOINT_SOCKET` environment
variable must point to the Workload API socket.

The Broker is used to acquire JWT-SVIDs for object-level workload identity when
a ServiceAccount sets `credential.auth.fluxcd.io/provider` to `spiffe`. The
Broker endpoint is a TCP gRPC target, e.g.
`dns:///spire-server.spire.svc.cluster.local:8443`.

Each `--spiffe-broker-id` value is interpreted like the SPIFFE IDs used by the
mTLS flags. Invalid values cause kustomize-controller to fail at startup.

### Override managers before dry-run

`--override-manager` takes over the named field managers only after a
successful server-side apply dry-run. If a stale manager co-owns a field in a
way that makes the merged object invalid (e.g. `containers[1].image: Required
value`), the dry-run fails on every reconcile and the takeover never runs.

`--override-manager-before-dry-run` takes over the named managers before the
dry-run. Use it only to recover stuck objects and remove it once they have
reconciled, because:

- All fields owned by the listed managers are transferred to
  kustomize-controller, and fields missing from the source are pruned.
- The managedFields change is not reverted if the apply fails.

Manager names are matched exactly, not by prefix.

### Feature Gates

| Name                             | Default Value | Description                                                                                                                                                                                                                                                             |
|----------------------------------|---------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `AdditiveCELDependencyCheck`     | `false`       | Run both the built-in health checks and the CEL expression `readyExpr` when `readyExpr` is configured on a Kustomization.                                                                                                                                               |
| `CacheSecretsAndConfigMaps`      | `false`       | Configures the caching of Secrets and ConfigMaps by the controller-runtime client. When enabled, it will cache both object types, resulting in increased memory usage.                                                                                                  |
| `CancelHealthCheckOnNewRevision` | `false`       | Cancels ongoing health checks when the desired state changes.                                                                                                                                                                                                          |
| `DirectSourceFetch`              | `false`       | Enables fetching source objects (GitRepository, OCIRepository, Bucket) directly from the API server using APIReader, bypassing the controller's cache. This can be useful when immediate consistency is required for source object reads.                               |
| `DisableCommitStatusEvent`      | `false`       | Disables the reconciliation success event used by notification-controller to update the Git commit status.                                                                                                                                                              |
| `DisableConfigWatchers`          | `false`       | Disables the watchers for ConfigMaps and Secrets.                                                                                                                                                                                                                       |
| `DisableFailFastBehavior`        | `false`       | Controls whether the fail-fast behavior when waiting for resources to become ready should be disabled.                                                                                                                                                                  |
| `DisableStatusPollerCache`       | `true`        | Disables the cache of the status poller, which is used to determine the health of the resources applied by the controller. This may have a positive impact on memory usage on large clusters with many objects, at the cost of an increased number of direct API calls. |
| `ExternalArtifact`               | `true`        | Enables support for the [ExternalArtifact](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1/externalartifacts.md) source type.                                                                                                                        |
| `GroupChangeLog`                 | `false`       | Groups together kubernetes objects in log output. Reduces cardinality for Elasticsearch/Opensearch indexing                                                                                                                                                             |
| `MigrateAPIVersion`              | `false`       | Migrates the API version referenced by the managed fields entries of in-cluster objects to the API version of the applied objects when they differ. Works around server-side apply dry-run failures like `field not declared in schema` after CRD upgrades.            |
| `StrictPostBuildSubstitutions`   | `true`        | Controls whether the post-build substitutions should fail if a variable without a default value is declared in files but is missing from the input vars.                                                                                                                |
