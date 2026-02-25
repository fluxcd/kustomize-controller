# Kustomization

<!-- menuweight:10 -->

The `Kustomization` API defines a pipeline for fetching, decrypting, building,
validating and applying Kustomize overlays or plain Kubernetes manifests.
The `Kustomization` Custom Resource Definition is the
counterpart of Kustomize's `kustomization.yaml` config file.

## Example

The following is an example of a Flux Kustomization that reconciles the
Kubernetes manifests stored in a Git repository.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: podinfo
  namespace: default
spec:
  interval: 5m
  url: https://github.com/stefanprodan/podinfo
  ref:
    branch: master
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: podinfo
  namespace: default
spec:
  interval: 10m
  targetNamespace: default
  sourceRef:
    kind: GitRepository
    name: podinfo
  path: "./kustomize"
  prune: true
  timeout: 1m
```

In the above example:

- A Flux GitRepository named `podinfo` is created that clones the `master`
  branch and makes the repository content available as an Artifact inside the cluster.
- A Flux Kustomization named `podinfo` is created that watches the
  GitRepository for Artifact changes.
- The Kustomization builds the YAML manifests located at the specified `.spec.path`,
  sets the namespace of all objects to the `.spec.targetNamespace`,
  validates the objects against the Kubernetes API and finally applies them on
  the cluster.
- As specified by `.spec.interval`, every ten minutes, the Kustomization runs a
  server-side apply dry-run to detect and correct drift inside the cluster.
- When the Git revision changes, the manifests are reconciled automatically. If
  previously applied objects are missing from the current revision, these
  objects are deleted from the cluster when `.spec.prune` is enabled.

You can run this example by saving the manifest into `podinfo.yaml`.

1. Apply the resource on the cluster:

   ```sh
   kubectl apply -f podinfo.yaml
   ```

2. Run `kubectl get gitrepositories` to see the source status:

   ```console
   NAME      URL                                       READY   STATUS
   podinfo   https://github.com/stefanprodan/podinfo   True    stored artifact for revision 'master@sha1:450796ddb2ab6724ee1cc32a4be56da032d1cca0'
   ```

3. Run `kubectl get kustomizations` to see the reconciliation status:

   ```console
   NAME      READY   STATUS
   podinfo   True    Applied revision: master@sha1:450796ddb2ab6724ee1cc32a4be56da032d1cca0
   ```

4. Run `kubectl describe kustomization podinfo` to see the reconciliation status
   conditions and events:

   ```console
   ...
    Status:
      Conditions:
        Last Transition Time:  2023-03-07T11:14:41Z
        Message:               Applied revision: master@sha1:450796ddb2ab6724ee1cc32a4be56da032d1cca0
        Reason:                ReconciliationSucceeded
        Status:                True
        Type:                  Ready
    Events:
      Type    Reason       Age    From                  Message
      ----    ------       ----   ----                  -------
      Normal  Progressing  1m48s  kustomize-controller  Service/default/podinfo created
                                                        Deployment/default/podinfo created
                                                        HorizontalPodAutoscaler/default/podinfo created
      Normal  ReconciliationSucceeded  1m48s  kustomize-controller  Reconciliation finished in 176.163666ms, next run in 10m0s
   ```

## Writing a Kustomization spec

As with all other Kubernetes config, a Kustomization needs `apiVersion`,
`kind`, and `metadata` fields. The name of a Kustomization object must be a
valid [DNS subdomain name](https://kubernetes.io/docs/concepts/overview/working-with-objects/names#dns-subdomain-names).

A Kustomization also needs a
[`.spec` section](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#spec-and-status).

### Source reference

`.spec.sourceRef` is used to refer to the Source object which has the required
Artifact containing the YAML manifests. It has two required fields:

- `kind`: The Kind of the referred Source object. Supported Source types:
  + [GitRepository](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1/gitrepositories.md)
  + [OCIRepository](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1/ocirepositories.md)
  + [Bucket](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1/buckets.md)
  + [ExternalArtifact](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1/externalartifacts.md) (requires `--feature-gates=ExternalArtifact=true` flag)
- `name`: The Name of the referred Source object.

#### Cross-namespace references

By default, the Source object is assumed to be in the same namespace as the
Kustomization. To refer to a Source object in a different namespace, specify
the namespace using `.spec.sourceRef.namespace`.

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: webapp
  namespace: apps
spec:
  interval: 5m
  path: "./deploy"
  sourceRef:
    kind: GitRepository
    name: webapp
    namespace: shared
```

On multi-tenant clusters, platform admins can disable cross-namespace references
by starting kustomize-controller with the `--no-cross-namespace-refs=true` flag.

### Prune

`.spec.prune` is a required boolean field to enable/disable garbage collection
for a Kustomization.

Garbage collection means that the Kubernetes objects that were previously
applied on the cluster but are missing from the current source revision, are
removed from the cluster automatically. Garbage collection is also performed
when a Kustomization object is deleted, triggering a removal of all Kubernetes
objects previously applied on the cluster. The removal of the Kubernetes
objects is done in the background, i.e. it doesn't block the reconciliation of
the Kustomization.

To enable garbage collection for a Kustomization, set this field to `true`.

You can disable pruning for certain resources by either labelling or
annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/prune: disabled
```

For details on how the controller tracks Kubernetes objects and determines what
to garbage collect, see [`.status.inventory`](#inventory).

### Deletion policy

`.spec.deletionPolicy` is an optional field that allows control over
garbage collection when a Kustomization object is deleted. The default behavior
is to mirror the configuration of [`.spec.prune`](#prune).

Valid values:

- `MirrorPrune` (default) - The managed resources will be deleted if `prune` is
  `true` and orphaned if `false`.
- `Delete` - Ensure the managed resources are deleted before the Kustomization
   is deleted.
- `WaitForTermination` - Ensure the managed resources are deleted and wait for
  termination before the Kustomization is deleted.
- `Orphan` - Leave the managed resources when the Kustomization is deleted.

The `WaitForTermination` deletion policy blocks and waits for the managed
resources to be removed from etcd by the Kubernetes garbage collector.
The wait time is determined by the `.spec.timeout` field. If a timeout occurs,
the controller will stop waiting for the deletion of the resources,
log an error and will allow the Kustomization to be deleted.

For special cases when the managed resources are removed by other means (e.g.
the deletion of the namespace specified with
[`.spec.targetNamespace`](#target-namespace)), you can set the deletion policy
to `Orphan`:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: app
  namespace: default
spec:
  # ...omitted for brevity
  targetNamespace: app-namespace
  prune: true
  deletionPolicy: Orphan
```

### Interval

`.spec.interval` is a required field that specifies the interval at which the
Kustomization is reconciled, i.e. the controller fetches the source with the
Kubernetes manifests, builds the Kustomization and applies it on the cluster,
correcting any existing drift in the process. The minimum value should be 60
seconds.

After successfully reconciling the object, the controller requeues it for
inspection after the specified interval. The value must be in a
[Go recognized duration string format](https://pkg.go.dev/time#ParseDuration),
e.g. `10m0s` to reconcile the object every 10 minutes.

If the `.metadata.generation` of a resource changes (due to e.g. a change to
the spec) or the Source revision changes (which generates a Kubernetes event),
this is handled instantly outside the interval window.

**Note:** The controller can be configured to apply a jitter to the interval in
order to distribute the load more evenly when multiple Kustomization objects are
set up with the same interval. For more information, please refer to the
[kustomize-controller configuration options](https://fluxcd.io/flux/components/kustomize/options/).

### Retry interval

`.spec.retryInterval` is an optional field to specify the interval at which to
retry a failed reconciliation. Unlike `.spec.interval`, this field is
exclusively meant for failure retries. If not specified, it defaults to
`.spec.interval`.

### Path

`.spec.path` is an optional field to specify the path to the directory in the
Source Artifact containing the `kustomization.yaml` file, or the set of plain
YAMLs for which a `kustomization.yaml` should be generated.
It defaults to blank, which translates to the root of the Source Artifact.

For more details on the generation of the file, see [generating a
`kustomization.yaml` file](#generating-a-kustomizationyaml-file).

### Target namespace

`.spec.targetNamespace` is an optional field to specify the target namespace for
all the objects that are part of the Kustomization. It either configures or
overrides the [Kustomize `namespace`](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/namespace/).

While `.spec.targetNamespace` is optional, if this field is non-empty then the
Kubernetes namespace being pointed to must exist prior to the Kustomization
being applied or be defined by a manifest included in the Kustomization.
kustomize-controller will not create the namespace automatically.

### Suspend

`.spec.suspend` is an optional boolean field to suspend the reconciliation of the
Kustomization. When a Kustomization is suspended, new Source revisions are not
applied to the cluster and drift detection/correction is paused.
To resume normal reconciliation, set it back to `false` or remove the field.

For more information, see [suspending and resuming](#suspending-and-resuming).

### Health checks

`.spec.healthChecks` is an optional list used to refer to resources for which the
controller will perform health checks used to determine the rollout status of
[deployed workloads](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status)
and the `Ready` status of custom resources.

A health check entry can reference one of the following types:

- Kubernetes built-in kinds: Deployment, DaemonSet, StatefulSet,
  PersistentVolumeClaim, Pod, PodDisruptionBudget, Job, CronJob, Service,
  Secret, ConfigMap, CustomResourceDefinition
- Flux kinds: HelmRelease, HelmRepository, GitRepository, etc.
- Custom resources that are compatible with [kstatus](https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus)

Assuming the Kustomization source contains a Kubernetes Deployment named
`backend`, a health check can be defined as follows:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: backend
  namespace: default
spec:
  interval: 5m
  prune: true
  sourceRef:
    kind: GitRepository
    name: webapp
  healthChecks:
    - apiVersion: apps/v1
      kind: Deployment
      name: backend
      namespace: dev
```

After applying the kustomize build output, the controller verifies if the
rollout was completed successfully. If the deployment was successful, the
Kustomization `Ready` condition is marked as `True`, if the rollout failed,
or if it takes more than the specified timeout to complete, then the
Kustomization `Ready` condition is set to `False`. If the deployment becomes
healthy on the next execution, then the Kustomization is marked as ready.

When a Kustomization contains HelmRelease objects, instead of checking the
underlying Deployments, you can define a health check that waits for the
HelmReleases to be reconciled with:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: webapp
  namespace: default
spec:
  interval: 15m
  path: "./releases/"
  prune: true
  sourceRef:
    kind: GitRepository
    name: webapp
  healthChecks:
    - apiVersion: helm.toolkit.fluxcd.io/v2
      kind: HelmRelease
      name: frontend
      namespace: dev
    - apiVersion: helm.toolkit.fluxcd.io/v2
      kind: HelmRelease
      name: backend
      namespace: dev
  timeout: 5m
```

If all the HelmRelease objects are successfully installed or upgraded, then
the Kustomization will be marked as ready.

### Health check expressions

`.spec.healthCheckExprs` can be used to define custom logic for performing
health checks on custom resources. This is done through Common Expression
Language (CEL) expressions. This field accepts a list of objects with the
following fields:

- `apiVersion`: The API version of the custom resource. Required.
- `kind`: The kind of the custom resource. Required.
- `current`: A required CEL expression that returns `true` if the resource is ready.
- `inProgress`: An optional CEL expression that returns `true` if the resource
  is still being reconciled.
- `failed`: An optional CEL expression that returns `true` if the resource
  failed to reconcile.

The controller will evaluate the expressions in the following order:

1. `inProgress` if specified
2. `failed` if specified
3. `current`

The first expression that evaluates to `true` will determine the health
status of the custom resource.

For example, to define a set of health check expressions for the `SealedSecret`
custom resource:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: sealed-secrets
  namespace: flux-system
spec:
  interval: 5m
  path: ./path/to/sealed/secrets
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
  timeout: 1m
  wait: true # Tells the controller to wait for all resources to be ready by performing health checks.
  healthCheckExprs:
    - apiVersion: bitnami.com/v1alpha1
      kind: SealedSecret
      failed: status.conditions.filter(e, e.type == 'Synced').all(e, e.status == 'False')
      current: status.conditions.filter(e, e.type == 'Synced').all(e, e.status == 'True')
```

A common error is writing expressions that reference fields that do not
exist in the custom resource. This will cause the controller to wait
for the resource to be ready until the timeout is reached. To avoid this,
make sure your CEL expressions are correct. The
[CEL Playground](https://playcel.undistro.io/) is a useful resource for
this task. The input passed to each expression is the custom resource
object itself. You can check for field existence with the
[`has(...)` CEL macro](https://github.com/google/cel-spec/blob/master/doc/langdef.md#macros),
just be aware that `has(status)` errors if `status` does not (yet) exist
on the top level of the resource you are using.

It's worth checking if [the library](/flux/cheatsheets/cel-healthchecks/)
has expressions for the custom resources you are using.

### Wait

`.spec.wait` is an optional boolean field to perform health checks for __all__
reconciled resources as part of the Kustomization. If set to `true`,
`.spec.healthChecks` is ignored.

### Timeout

`.spec.timeout` is an optional field to specify a timeout duration for any
operation like building, applying, health checking, etc. performed during the
reconciliation process.

### Dependencies

`.spec.dependsOn` is an optional list used to refer to other Kustomization
objects that the Kustomization depends on. If specified, then the Kustomization
is only applied after the referred Kustomizations are ready, i.e. have the
`Ready` condition marked as `True`. The readiness state of a Kustomization is
determined by its last applied status condition.

This is helpful when there is a need to make sure other resources exist before
the workloads defined in a Kustomization are deployed. For example, before
installing objects of a certain custom resource kind, the CRDs and the related
controller must exist in the cluster.

For example, assuming we have two Kustomizations:

- cert-manager: reconciles the cert-manager CRDs and controller
- certs: reconciles the cert-manager custom resources

You can instruct the controller to apply the `cert-manager` Kustomization before
`certs` by defining a `dependsOn` relationship between the two:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: cert-manager
  namespace: flux-system
spec:
  interval: 5m
  path: "./cert-manager/controller"
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
  healthChecks:
    - apiVersion: apps/v1
      kind: Deployment
      name: cert-manager
      namespace: cert-manager
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: certs
  namespace: flux-system
spec:
  dependsOn:
    - name: cert-manager
  interval: 5m
  path: "./cert-manager/certs"
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
```

If `.spec.healthChecks` is non-empty or `.spec.wait` is set to `true`, a
Kustomization will be applied after all its dependencies' health checks have
passed. For example, this can be used to ensure a service mesh proxy injector
is running before deploying applications inside the mesh.

**Note:** Circular dependencies between Kustomizations must be avoided,
otherwise the interdependent Kustomizations will never be applied on the cluster.

#### Dependency Ready Expression

`.spec.dependsOn[].readyExpr` is an optional field that can be used to define a CEL expression
to determine the readiness of a Kustomization dependency. 

This is helpful for when custom logic is needed to determine if a dependency is ready.
For example, when performing a lockstep upgrade, the `readyExpr` can be used to
verify that a dependency has a matching version label before proceeding with the
reconciliation of the dependent Kustomization.

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: app-backend
  namespace: apps
  labels:
    app/version: v1.2.3
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: app-frontend
  namespace: apps
  labels:
    app/version: v1.2.3
spec:
  dependsOn:
    - name: app-backend
      readyExpr: >
        dep.metadata.labels['app/version'] == self.metadata.labels['app/version'] &&
        dep.status.conditions.filter(e, e.type == 'Ready').all(e, e.status == 'True') &&
        dep.metadata.generation == dep.status.observedGeneration
```

The CEL expression contains the following variables:

- `dep`: The dependency Kustomization object being evaluated.
- `self`: The Kustomization object being reconciled.

**Note:** When `readyExpr` is specified, the built-in readiness check is replaced by the logic
defined in the CEL expression. You can configure the controller to run both the CEL expression
evaluation and the built-in readiness check, with the `AdditiveCELDependencyCheck`
[feature gate](https://fluxcd.io/flux/components/kustomize/options/#feature-gates).

### Service Account reference

`.spec.serviceAccountName` is an optional field used to specify the
ServiceAccount to be impersonated while reconciling the Kustomization. For more
details, see [Role-based Access Control](#role-based-access-control).

### Common metadata

`.spec.commonMetadata` is an optional field used to specify any metadata that
should be applied to all the Kustomization's resources. It has two optional fields:

- `labels`: A map used for setting [labels](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/)
  on an object. Any existing label will be overridden if it matches with a key in
  this map.
- `annotations`: A map used for setting [annotations](https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/)
  on an object. Any existing annotation will be overridden if it matches with a key
  in this map.

### Name Prefix and Suffix

`.spec.namePrefix` and `.spec.nameSuffix` are optional fields used to specify a prefix and suffix
to be added to the names of all the resources in the Kustomization.

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: app
spec:
  # ...omitted for brevity
  namePrefix: "prefix-"
  nameSuffix: "-suffix"
```

### Patches

`.spec.patches` is an optional list used to specify [Kustomize `patches`](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/patches/)
as inline YAML objects. This enables patching resources using either a
[strategic merge](https://kubectl.docs.kubernetes.io/references/kustomize/glossary#patchstrategicmerge)
patch or a [JSON6902](https://kubectl.docs.kubernetes.io/references/kustomize/glossary#patchjson6902)
patch. A patch can target a single resource or multiple resources. Each item in
the list must have the two fields mentioned below:

- `patch`: Patch contains an inline strategic merge patch or an inline JSON6902 patch with an array of operation objects.
- `target`: Target points to the resources that the patch document should be applied to.

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  # ...omitted for brevity
  patches:
    - patch: |-
        apiVersion: apps/v1
        kind: Deployment
        metadata:
          name: not-used
        spec:
          template:
            metadata:
              annotations:
                cluster-autoscaler.kubernetes.io/safe-to-evict: "true"
      target:
        kind: Deployment
        labelSelector: "app.kubernetes.io/part-of=my-app"
    - patch: |
        - op: add
          path: /spec/template/spec/securityContext
          value:
            runAsUser: 10000
            fsGroup: 1337
        - op: add
          path: /spec/template/spec/containers/0/securityContext
          value:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            runAsNonRoot: true
            capabilities:
              drop:
                - ALL
      target:
        kind: Deployment
        name: podinfo
        namespace: apps
```

### Images

`.spec.images` is an optional list used to specify
[Kustomize `images`](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/images/).
This allows overwriting the name, tag or digest of container images without creating patches.

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  # ...omitted for brevity
  images:
  - name: podinfo
    newName: my-registry/podinfo
    newTag: v1
  - name: podinfo
    newTag: 1.8.0
  - name: podinfo
    newName: my-podinfo
  - name: podinfo
    digest: sha256:24a0c4b4a4c0eb97a1aabb8e29f18e917d05abfe1b7a7c07857230879ce7d3d3
```

### Components

`.spec.components` is an optional list used to specify
[Kustomize `components`](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/components/).
This allows using reusable pieces of configuration logic that can be included
from multiple overlays.

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  # ...omitted for brevity
  ignoreMissingComponents: false
  components:
  - ../ingress
  - ../tls
```

**Note:** The components paths must be local and relative to the path specified by `.spec.path`.

With `.spec.ignoreMissingComponents` you can specify whether the controller
should ignore the component paths that are missing from the source. By default,
it is set to `false`, meaning that the controller will fail the reconciliation
if any of the specified paths are missing from the source.

**Warning:** Components are an alpha feature in Kustomize and are therefore
considered experimental in Flux. No guarantees are provided as the feature may
be modified in backwards incompatible ways or removed without warning.

### Post build variable substitution

With `.spec.postBuild.substitute` you can provide a map of key-value pairs
holding the variables to be substituted in the final YAML manifest, after
kustomize build.

With `.spec.postBuild.substituteFrom` you can provide a list of ConfigMaps and
Secrets from which the variables are loaded. The ConfigMap and Secret data keys
are used as the variable names.

To make a Kustomization react immediately to changes in the referenced Secret
or ConfigMap see [this](#reacting-immediately-to-configuration-dependencies)
section.

The `.spec.postBuild.substituteFrom.optional` field indicates how the
controller should handle a referenced ConfigMap or Secret being absent
at reconciliation time. The controller's default behavior ― with
`optional` unspecified or set to `false` ― it has failed reconciliation if
the referenced object is missing. By setting the `optional` field to
`true`, you can indicate that the controller should use the referenced
object if it's there, but also tolerate its absence, treating that
absence as if the object had been present but empty, defining no
variables.

This offers basic templating for your manifests including support
for [bash string replacement functions](https://github.com/fluxcd/pkg/blob/main/envsubst/README.md) e.g.:

- `${var:=default}`
- `${var:position}`
- `${var:position:length}`
- `${var/substring/replacement}`

**Note:** The name of a variable can contain only alphanumeric and underscore
characters. The controller validates the variable names using this regular
expression: `^[_[:alpha:]][_[:alpha:][:digit:]]*$`.

For example, assuming we have manifests with the following variables:

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: apps
  labels:
    environment: ${cluster_env:=dev}
    region: "${cluster_region}"
```

You can specify the variables and their values in the Kustomization definition using
`.spec.postBuild.substitute` and/or `.spec.postBuild.substituteFrom`:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
spec:
  # ...omitted for brevity
  postBuild:
    substitute:
      cluster_env: "prod"
      cluster_region: "eu-central-1"
    substituteFrom:
      - kind: ConfigMap
        name: cluster-vars
        # Use this ConfigMap if it exists, but proceed if it doesn't.
        optional: true
      - kind: Secret
        name: cluster-secret-vars
        # Fail if this Secret does not exist.
```

For substituting variables in a secret, `.spec.stringData` field must be used i.e:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: secret
  namespace: flux-system
type: Opaque
stringData:
  token: ${token}
```

The var values which are specified in-line with `substitute`
take precedence over the ones derived from `substituteFrom`.
When var values for the same variable keys are derived from multiple
`ConfigMaps` or `Secrets` referenced in the `substituteFrom` list,
the later values overwrite earlier values.

**Note:** If you want to avoid var substitutions in scripts embedded in
ConfigMaps or container commands, you must use the format `$var` instead of
`${var}`. If you want to keep the curly braces you can use `$${var}` which
will print out `${var}`.

All the undefined variables in the format `${var}` will be substituted with an
empty string unless a default value is provided e.g. `${var:=default}`.

**Note:** It is recommended to set the `--feature-gates=StrictPostBuildSubstitutions=true`
controller flag, so that the post-build substitutions will fail if a
variable without a default value is declared in files but is
missing from the input vars.

You can disable the variable substitution for certain resources by either
labelling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/substitute: disabled
```

Substitution of variables only happens if at least a single variable or resource
to substitute from is defined. This may cause issues if you rely on expressions
which should evaluate to a default value, even if no other variables are
configured. To work around this, one can set an arbitrary key/value pair to
enable the substitution of variables. For example:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
spec:
  # ...omitted for brevity
  postBuild:
    substitute:
      var_substitution_enabled: "true"
```

**Note:** When using numbers or booleans as values for variables, they must be
enclosed in double quotes vars to be treated as strings, for more information see
[substitution of numbers and booleans](#post-build-substitution-of-numbers-and-booleans).

You can replicate the controller post-build substitutions locally using
[kustomize](https://github.com/kubernetes-sigs/kustomize)
and the Flux CLI:

```console
$ export cluster_region=eu-central-1
$ kustomize build ./apps/ | flux envsubst --strict
---
apiVersion: v1
kind: Namespace
metadata:
  name: apps
  labels:
    environment: dev
    region: eu-central-1
```

### Force

`.spec.force` is an optional boolean field. If set to `true`, the controller
will replace the resources in-cluster if the patching fails due to immutable
field changes.

Note that this field should be set temporarily (e.g. when changing Deployment
pod selectors) and not left enabled on the long run, as force-replacing
resources may cause downtime.

A safer alternative is to enable force-apply for specific resources by
labelling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/force: enabled
```

This way, only the targeted resources are force-replaced when immutable field
changes are made. The annotation should be removed after the change is applied.

### KubeConfig (Remote clusters)

With the `.spec.kubeConfig` field a Kustomization
can apply and manage resources on a remote cluster.

Two authentication alternatives are available:

- `.spec.kubeConfig.secretRef`: Secret-based authentication using a
  static kubeconfig stored in a Kubernetes Secret in the same namespace
  as the Kustomization.
- `.spec.kubeConfig.configMapRef` (Recommended): Secret-less authentication
  building a kubeconfig dynamically with parameters stored in a Kubernetes
  ConfigMap in the same namespace as the Kustomization via workload identity.

To make a Kustomization react immediately to changes in the referenced Secret
or ConfigMap see [this](#reacting-immediately-to-configuration-dependencies)
section.

When both `.spec.kubeConfig` and
[`.spec.serviceAccountName`](#service-account-reference) are specified,
the controller will impersonate the ServiceAccount on the target cluster,
i.e. a ServiceAccount with name `.spec.serviceAccountName` must exist in
the target cluster inside a namespace with the same name as the namespace
of the Kustomization. For example, if the Kustomization is in the namespace
`apps` of the cluster where Flux is running, then the ServiceAccount
must be in the `apps` namespace of the target remote cluster, and have the
name `.spec.serviceAccountName`. In other words, the namespace of the
Kustomization must exist both in the cluster where Flux is running
and in the target remote cluster where Flux will apply resources.

#### Secret-based authentication

`.spec.kubeConfig.secretRef.Name` is an optional field to specify the name of
the secret containing a KubeConfig. If specified, objects will be applied,
health-checked, pruned, and deleted for the default cluster specified in that
KubeConfig instead of using the in-cluster ServiceAccount.

The secret defined in the `.spec.kubeConfig.secretRef` must exist in the same
namespace as the Kustomization. On every reconciliation, the KubeConfig bytes
will be loaded from the `.secretRef.key` key (default: `value` or `value.yaml`)
of the Secret’s data , and the Secret can thus be regularly updated if
cluster-access-tokens have to rotate due to expiration.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: prod-kubeconfig
type: Opaque
stringData:
  value.yaml: |
    apiVersion: v1
    kind: Config
    # ...omitted for brevity
```

**Note:** The KubeConfig should be self-contained and not rely on binaries,
environment, or credential files from the kustomize-controller Pod.
This matches the constraints of KubeConfigs from current Cluster API providers.
KubeConfigs with `cmd-path` in them likely won't work without a custom,
per-provider installation of kustomize-controller. For more information, see
[remote clusters/Cluster-API](#remote-cluster-api-clusters).

#### Secret-less authentication

The field `.spec.kubeConfig.configMapRef.name` can be used to specify the
name of a ConfigMap in the same namespace as the Kustomization containing
parameters for secret-less authentication via workload identity. The
supported keys inside the `.data` field of the ConfigMap are:

- `.data.provider`: The provider to use. One of `aws`, `azure`, `gcp`,
  or `generic`. Required. The `aws` provider is used for connecting to
  remote EKS clusters, `azure` for AKS, `gcp` for GKE, and `generic`
  for Kubernetes OIDC authentication between clusters. For the
  `generic` provider, the remote cluster must be configured to trust
  the OIDC issuer of the cluster where Flux is running.
- `.data.cluster`: The fully qualified resource name of the Kubernetes
  cluster in the cloud provider API. Not used by the `generic`
  provider. Required when one of `.data.address` or `.data["ca.crt"]` is
  not set, or if the provider is `aws` (required for defining a region).
- `.data.address`: The address of the Kubernetes API server. Required
  for `generic`. For the other providers, if not specified, the
  first address in the cluster resource will be used, and if
  specified, it must match one of the addresses in the cluster
  resource.
  If `audiences` is not set, will be used as the audience for the
  `generic` provider.
- `.data["ca.crt"]`: The optional PEM-encoded CA certificate for the
  Kubernetes API server. If not set, the controller will use the
  CA certificate from the cluster resource.
- `.data.audiences`: The optional audiences as a list of
  line-break-separated strings for the Kubernetes ServiceAccount token.
  Defaults to the address for the `generic` provider, or to specific
  values for the other providers depending on the provider.
- `.data.serviceAccountName`: The optional name of the Kubernetes
  ServiceAccount in the same namespace that should be used
  for authentication. If not specified, the controller
  ServiceAccount will be used. Not confuse with the ServiceAccount
  used for impersonation, which is specified with
  [`.spec.serviceAccountName`](#service-account-reference) directly
  in the Kustomization spec and must exist in the target remote cluster.

The `.data.cluster` field, when specified, must have the following formats:

- `aws`: `arn:<partition>:eks:<region>:<account-id>:cluster/<cluster-name>`
- `azure`: `/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.ContainerService/managedClusters/<cluster-name>`
- `gcp`: `projects/<project-id>/locations/<location>/clusters/<cluster-name>`

For complete guides on workload identity and setting up permissions for
this feature, see the following docs:

- [EKS](/flux/integrations/aws/#for-amazon-elastic-kubernetes-service)
- [AKS](/flux/integrations/azure/#for-azure-kubernetes-service)
- [GKE](/flux/integrations/gcp/#for-google-kubernetes-engine)
- [Generic](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#configuring-the-api-server)

Example for an EKS cluster:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: backend
  namespace: apps
spec:
  ... # other fields omitted for brevity
  kubeConfig:
    configMapRef:
      name: kubeconfig
  serviceAccountName: apps-sa # optional. must exist in the target cluster. user for impersonation
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubeconfig
  namespace: apps
data:
  provider: aws
  cluster: arn:aws:eks:eu-central-1:123456789012:cluster/my-cluster
  serviceAccountName: apps-iam-role # optional. maps to an AWS IAM Role. used for authentication
```

### Decryption

Storing Secrets in Git repositories in plain text or base64 is unsafe,
regardless of the visibility or access restrictions of the repository.

In order to store Secrets safely in Git repositorioes you can use an
encryption provider and the optional field `.spec.decryption` to
configure decryption for Secrets that are a part of the Kustomization.

The only supported encryption provider is [SOPS](https://getsops.io/).
With SOPS you can encrypt your secrets with [age](https://github.com/FiloSottile/age)
or [OpenPGP](https://www.openpgp.org) keys, or with keys from Key Management Services
(KMS), like AWS KMS, Azure Key Vault, GCP KMS or Hashicorp Vault.

**Note:** You must leave `metadata`, `kind` or `apiVersion` in plain text.
An easy way to do this is limiting the encrypted keys with the flag
`--encrypted-regex '^(data|stringData)$'` in your `sops encrypt` command.

The `.spec.decryption` field has the following subfields:

- `.provider`: The secrets decryption provider to be used. This field is required and
  the only supported value is `sops`.
- `.secretRef.name`: The name of the secret that contains the keys or cloud provider
  static credentials for KMS services to be used for decryption.
- `.serviceAccountName`: The name of the service account used for
  secret-less authentication with KMS services from cloud providers.

To make a Kustomization react immediately to changes in the referenced Secret
see [this](#reacting-immediately-to-configuration-dependencies) section.

For a complete guide on how to set up authentication for KMS services from
cloud providers, see the integration [docs](/flux/integrations/).

If a static credential for a given cloud provider is defined inside the secret
referenced by `.secretRef`, that static credential takes priority over secret-less
authentication for that provider. If no static credentials are defined for a given
cloud provider inside the secret, secret-less authentication is attempted for that
provider.

If `.serviceAccountName` is specified for secret-less authentication,
it takes priority over [controller global decryption](#controller-global-decryption)
for all cloud providers.

Example:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: sops-encrypted
  namespace: default
spec:
  interval: 5m
  path: "./"
  sourceRef:
    kind: GitRepository
    name: repository-with-secrets
  decryption:
    provider: sops
    serviceAccountName: sops-identity
    secretRef:
      name: sops-keys-and-credentials
```

The Secret's `.data` section is expected to contain entries with decryption
keys (for age and OpenPGP), or credentials (for any of the supported provider
implementations). The controller identifies the type of the entry by the suffix
of the key (e.g. `.agekey`), or a fixed key (e.g. `sops.vault-token`).

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys-and-credentials
  namespace: default
data:
  # Exemplary age private key
  identity.agekey: <BASE64>
  # Exemplary Hashicorp Vault token
  sops.vault-token: <BASE64>
```

#### age Secret entry

To specify an age private key in a Kubernetes Secret, suffix the key of the
`.data` entry with `.agekey`.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
data:
  # Exemplary age private key
  identity.agekey: <BASE64>
```

#### OpenPGP Secret entry

To specify an OpenPGP (passwordless) keyring in armor format in a Kubernetes
Secret, suffix the key of the `.data` entry with `.asc`.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
data:
  # Exemplary OpenPGP keyring
  identity.asc: <BASE64>
```

#### AWS KMS Secret entry

To specify credentials for an AWS user account linked to the IAM role with access
to KMS, append a `.data` entry with a fixed `sops.aws-kms` key.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
data:
  sops.aws-kms: |
    aws_access_key_id: some-access-key-id
    aws_secret_access_key: some-aws-secret-access-key
    aws_session_token: some-aws-session-token # this field is optional
```

#### Azure Key Vault Secret entry

To specify credentials for Azure Key Vault in a Secret, append a `.data` entry
with a fixed `sops.azure-kv` key. The value can contain a variety of JSON or
YAML formats depending on the authentication method you want to utilize.

##### Service Principal with Secret

To configure a Service Principal with Secret credentials to access the Azure
Key Vault, a JSON or YAML object with `tenantId`, `clientId` and `clientSecret`
fields must be configured as the `sops.azure-kv` value. It optionally supports
`authorityHost` to configure an authority host other than the Azure Public Cloud
endpoint.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
stringData:
  # Exemplary Azure Service Principal with Secret
  sops.azure-kv: |
    tenantId: some-tenant-id
    clientId: some-client-id
    clientSecret: some-client-secret
```

##### Service Principal with Certificate

To configure a Service Principal with Certificate credentials to access the
Azure Key Vault, a JSON or YAML object with `tenantId`, `clientId` and
`clientCertificate` fields must be configured as the `sops.azure-kv` value.
It optionally supports `clientCertificateSendChain` and `authorityHost` to
control the sending of the certificate chain, or to specify an authority host
other than the Azure Public Cloud endpoint.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
stringData:
  # Exemplary Azure Service Principal with Certificate
  sops.azure-kv: |
    tenantId: some-tenant-id
    clientId: some-client-id
    clientCertificate: <certificate PEM>
```

##### `az` generated Service Principal

To configure a Service Principal [generated using
`az`](https://docs.microsoft.com/en-us/azure/aks/kubernetes-service-principal?tabs=azure-cli#manually-create-a-service-principal),
the output of the command can be directly used as a `sops.azure-kv` value.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
stringData:
  # Exemplary Azure Service Principal generated with `az`
  sops.azure-kv: |
    {
      "appId": "559513bd-0c19-4c1a-87cd-851a26afd5fc",
      "displayName": "myAKSClusterServicePrincipal",
      "name": "http://myAKSClusterServicePrincipal",
      "password": "e763725a-5eee-40e8-a466-dc88d980f415",
      "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db48"
    }
```

##### Managed Identity with Client ID

To configure a Managed Identity making use of a Client ID, a JSON or YAML
object with a `clientId` must be configured as the `sops.azure-kv` value. It
optionally supports `authorityHost` to configure an authority host other than
the Azure Public Cloud endpoint.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
stringData:
  # Exemplary Azure Managed Identity with Client ID
  sops.azure-kv: |
    clientId: some-client-id
```

#### GCP KMS Secret entry

To specify credentials for GCP KMS in a Kubernetes Secret, append a `.data`
entry with a fixed `sops.gcp-kms` key and the service account keys as its value.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
stringData:
  # Exemplary GCP Service Account credentials file
  sops.gcp-kms: |
    {
      "type": "service_account",
      "project_id": "<project-id>",
      "private_key_id": "<private-key-id>",
      "private_key": "<private-key>"
    }
```

#### Hashicorp Vault Secret entry

To specify credentials for Hashicorp Vault in a Kubernetes Secret, append a
`.data` entry with a fixed `sops.vault-token` key and the token as value.

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-keys
  namespace: default
data:
  # Exemplary Hashicorp Vault Secret token
  sops.vault-token: <BASE64>
```

#### Controlling the decryption behavior of resources

To change the decryption behaviour for specific Kubernetes resources, you can annotate them with:

| Annotation                          | Default    | Values                                                         | Role            |
|-------------------------------------|------------|----------------------------------------------------------------|-----------------|
| `kustomize.toolkit.fluxcd.io/decrypt` | `Enabled` | - `Enabled`<br/>- `Disabled`                                   | Decryption policy |

##### Disabled

The `Disabled` policy instructs the controller to not decrypt Kubernetes resources. This might be useful if there is another entity that is going to decrpyt the resource later.

## Working with Kustomizations

### Recommended settings

When deploying applications to production environments, it is recommended
to configure the following fields, while adjusting them to your desires for
responsiveness:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: webapp
  namespace: apps
spec:
  interval: 1m0s # check for new commits every minute and apply changes
  url: https://github.com/org/webapp # clone over HTTPS
  secretRef: # use token auth
    name: webapp-git-token # Flux user PAT (read-only access)
  ref:
    branch: main
  ignore: |
    # exclude all
    /*
    # include deploy dir
    !/deploy
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: webapp
  namespace: apps
spec:
  interval: 60m0s # detect drift and undo kubectl edits every hour
  wait: true # wait for all applied resources to become ready
  timeout: 3m0s # give up waiting after three minutes
  retryInterval: 2m0s # retry every two minutes on apply or waiting failures
  prune: true # remove stale resources from cluster
  force: false # enable this to recreate resources on immutable fields changes
  targetNamespace: apps # set the namespace for all resources
  sourceRef:
    kind: GitRepository
    name: webapp
    namespace: apps
  path: "./deploy/production"
```

### Generating a `kustomization.yaml` file

If your repository contains plain Kubernetes manifests without a
`kustomization.yaml`, the file is automatically generated for all the
Kubernetes manifests in the directory tree specified in [`.spec.path`](#path).

All YAML files present under that path must be valid Kubernetes manifests,
unless they're excluded either by way of the [`.sourceignore`](https://fluxcd.io/flux/components/source/gitrepositories/#sourceignore-file)
file or the [`.spec.ignore`](https://fluxcd.io/flux/components/source/gitrepositories/#ignore)
field on the corresponding Source object.

Example of excluding CI workflows and SOPS config files:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: podinfo
  namespace: default
spec:
  interval: 5m
  url: https://github.com/stefanprodan/podinfo
  ignore: |
    .github/
    .sops.yaml
    .gitlab-ci.yml
```

It is recommended to generate the `kustomization.yaml` on your own and store it
in Git, this way you can validate your manifests in CI
([example script](https://github.com/fluxcd/flux2-multi-tenancy/blob/main/scripts/validate.sh)).
Assuming your manifests are inside `apps/my-app`, you can generate a
`kustomization.yaml` with:

```sh
cd apps/my-app

# create kustomization.yaml
kustomize create --autodetect --recursive
```

### Controlling the apply behavior of resources

To change the apply behaviour for specific Kubernetes resources, you can annotate them with:

| Annotation                          | Default    | Values                                                         | Role            |
|-------------------------------------|------------|----------------------------------------------------------------|-----------------|
| `kustomize.toolkit.fluxcd.io/ssa`   | `Override` | - `Override`<br/>- `Merge`<br/>- `IfNotPresent`<br/>- `Ignore` | Apply policy    |
| `kustomize.toolkit.fluxcd.io/force` | `Disabled` | - `Enabled`<br/>- `Disabled`                                   | Recreate policy |
| `kustomize.toolkit.fluxcd.io/prune` | `Enabled`  | - `Enabled`<br/>- `Disabled`                                   | Delete policy   |

**Note:** These annotations should be set in the Kubernetes YAML manifests included
in the Flux Kustomization source (Git, OCI, Bucket).

#### `kustomize.toolkit.fluxcd.io/ssa`

##### Override

The `Override` policy instructs the controller to reconcile the Kubernetes resources
with the desired state (YAML manifests) defined in the Flux source (Git, OCI, Bucket).

If you use `kubectl` to edit a Kubernetes resource managed by Flux, all changes will be
reverted when the controller reconciles a Flux Kustomization containing that resource.
In order to preserve fields added with `kubectl`, you have to specify
a field manager named `flux-client-side-apply` e.g.:

```sh
kubectl apply --field-manager=flux-client-side-apply
```

##### Merge

The `Merge` policy instructs the controller to preserve the fields added by other tools to the
Kubernetes resources managed by Flux.

The fields defined in the manifests applied by the controller will always be overridden,
the `Merge` policy works only for adding new fields that don’t overlap with the desired
state.

For lists fields which are atomic (e.g. `.spec.tolerations` in PodSpec), Kubernetes
doesn't allow different managers for such fields, therefore any changes to these
fields will be reverted. For more context, please see the Kubernetes enhancement document:
[555-server-side-apply](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/555-server-side-apply/README.md#lists).

##### IfNotPresent

The `IfNotPresent` policy instructs the controller to only apply the Kubernetes resources
if they are not present on the cluster.

This policy can be used for Kubernetes Secrets and ValidatingWebhookConfigurations managed by cert-manager,
where Flux creates the resources with fields that are later on mutated by other controllers.

##### Ignore

The `Ignore` policy instructs the controller to skip applying Kubernetes resources
even if they are included in a Flux source (Git, OCI, Bucket).

#### `kustomize.toolkit.fluxcd.io/force`

When set to `Enabled`, this policy instructs the controller to recreate the Kubernetes resources
with changes to immutable fields.

This policy can be used for Kubernetes Jobs to rerun them when their container image changes.

**Note:** Using this policy for StatefulSets may result in potential data loss.

#### `kustomize.toolkit.fluxcd.io/prune`

When set to `Disabled`, this policy instructs the controller to skip the deletion of
the Kubernetes resources subject to [garbage collection](#prune).

This policy can be used to protect sensitive resources such as Namespaces, PVCs and PVs
from accidental deletion.

### Role-based access control

By default, a Kustomization apply runs under the cluster admin account and can
create, modify and delete cluster level objects (namespaces, CRDs, etc) and
namespaced objects (deployments, ingresses, etc). For certain Kustomizations a
cluster admin may wish to control what types of Kubernetes objects can be
reconciled and under which namespaces.
To restrict a Kustomization, one can assign a service account under which the
reconciliation is performed using [`.spec.serviceAccountName`](#service-account-reference).

Assuming you want to restrict a group of Kustomizations to a single namespace,
you can create an account with a role binding that grants access only to that namespace:

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: webapp
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: flux
  namespace: webapp
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: webapp-reconciler
  namespace: webapp
rules:
  - apiGroups: ['*']
    resources: ['*']
    verbs: ['*']
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: webapp-reconciler
  namespace: webapp
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: webapp-reconciler
subjects:
- kind: ServiceAccount
  name: flux
  namespace: webapp
```

**Note:** The namespace, RBAC and service account manifests should be
placed in a Git source and applied with a Kustomization. The Kustomizations that
are running under that service account should depend on the one that contains the account.

Create a Kustomization that prevents altering the cluster state outside the
`webapp` namespace:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: backend
  namespace: webapp
spec:
  serviceAccountName: flux
  dependsOn:
    - name: rbac
  interval: 5m
  path: "./webapp/backend/"
  prune: true
  sourceRef:
    kind: GitRepository
    name: webapp
```

When the controller reconciles the `backend` Kustomization, it will impersonate
the `flux` ServiceAccount. If the Kustomization contains cluster level objects
like CRDs or objects belonging to a different namespace, the reconciliation will
fail since the account it runs under has no permissions to alter objects outside
the `webapp` namespace.

#### Enforcing impersonation

On multi-tenant clusters, platform admins can enforce impersonation with the
`--default-service-account` flag.

When the flag is set, all Kustomizations which don't have [`.spec.serviceAccountName`](#service-account-reference)
specified will use the service account name provided by
`--default-service-account=<SA Name>` in the namespace of the object.

### Remote Cluster API clusters

Using a [`.spec.kubeConfig` reference](#kubeconfig-remote-clusters) a Kustomization can be fully
reconciled on a remote cluster. This composes well with Cluster API bootstrap
providers such as CAPBK (kubeadm), CAPA (AWS) and others.

To reconcile a Kustomization to a CAPI controlled cluster, put the
`Kustomization` in the same namespace as your `Cluster` object, and set the
`kubeConfig.secretRef.name` to `<cluster-name>-kubeconfig`:

```yaml
apiVersion: cluster.x-k8s.io/v1alpha3
kind: Cluster
metadata:
  name: stage  # the kubeconfig Secret will contain the Cluster name
  namespace: capi-stage
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
      - 10.100.0.0/16
    serviceDomain: stage-cluster.local
    services:
      cidrBlocks:
      - 10.200.0.0/12
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1alpha3
    kind: KubeadmControlPlane
    name: stage-control-plane
    namespace: capi-stage
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha3
    kind: DockerCluster
    name: stage
    namespace: capi-stage
---
# ... unrelated Cluster API objects omitted for brevity ...
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: cluster-addons
  namespace: capi-stage
spec:
  interval: 5m
  path: "./config/addons/"
  prune: true
  sourceRef:
    kind: GitRepository
    name: cluster-addons
  kubeConfig:
    secretRef:
      name: stage-kubeconfig  # Cluster API creates this for the matching Cluster
```

The Cluster and Kustomization can be created at the same time.
The Kustomization will eventually reconcile once the cluster is available.

If you wish to target clusters created by other means than CAPI, you can create
a ServiceAccount on the remote cluster, generate a KubeConfig for that account
and then create a secret on the cluster where kustomize-controller is running.
For example:

```sh
kubectl create secret generic prod-kubeconfig \
    --from-file=value.yaml=./kubeconfig
```

### Controller global decryption

Other than [authentication using a Secret reference](#decryption),
it is possible to specify global decryption settings on the
kustomize-controller Pod. When the controller fails to find credentials on the
Kustomization object itself, it will fall back to these defaults.

See also the [workload identity](/flux/installation/configuration/workload-identity/) docs.

#### Cloud Provider KMS Services

For cloud provider KMS services, please refer to the specific sections in the integration guides:

Service-specific configuration:

- [AWS KMS](https://fluxcd.io/flux/integrations/aws/#for-amazon-key-management-service)
- [Azure Key Vault](https://fluxcd.io/flux/integrations/azure/#for-azure-key-vault)
- [GCP KMS](https://fluxcd.io/flux/integrations/gcp/#for-google-cloud-key-management-service)

Controller-level configuration:

- [AWS](https://fluxcd.io/flux/integrations/aws/#at-the-controller-level)
- [Azure](https://fluxcd.io/flux/integrations/azure/#at-the-controller-level)
- [GCP](https://fluxcd.io/flux/integrations/gcp/#at-the-controller-level)

These guides provide detailed instructions for setting up authentication,
permissions, and controller configuration for each cloud provider.

#### Hashicorp Vault

To configure a global default for Hashicorp Vault, patch the controller's
Deployment with a `VAULT_TOKEN` environment variable.

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kustomize-controller
  namespace: flux-system
spec:
  template:
    spec:
      containers:
      - name: manager
        env:
        - name: VAULT_TOKEN
          value: <token>
```

#### SOPS Age Keys

To configure global decryption for SOPS Age keys, use the `--sops-age-secret`
controller flag to specify a Kubernetes Secret containing the Age private keys.

First, create a Secret containing the Age private keys with the `.agekey` suffix:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: sops-age-keys
  namespace: flux-system
stringData:
  identity1.agekey: <identity1 key>
  identity2.agekey: <identity1 key>
```

The Secret must be in the same namespace as the kustomize-controller Deployment.

Then, patch the kustomize-controller Deployment to add the `--sops-age-secret` flag:

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kustomize-controller
  namespace: flux-system
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --sops-age-secret=sops-age-keys
```

The field `.spec.decryption.secretRef` in the Kustomization will take precedence
in case both the controller flag and the Kustomization field are set.

### Kustomize secretGenerator

SOPS encrypted data can be stored as a base64 encoded Secret, which enables the
use of [Kustomize `secretGenerator`](https://github.com/kubernetes-sigs/kustomize/tree/main/examples/secretGeneratorPlugin.md)
as follows:

```console
$ echo "my-secret-token" | sops -e /dev/stdin > token.encrypted
$ cat <<EOF > kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

secretGenerator:
 - name: token
   files:
   - token=token.encrypted
EOF
```

Commit and push `token.encrypted` and `kustomization.yaml` to Git.

The kustomize-controller scans the values of Kubernetes Secrets, and when it
detects that the values are SOPS encrypted, it decrypts them before applying
them on the cluster.

For secrets in `.json`, `.yaml` `.ini` and `.env` format, make sure you specify
the input type when encrypting them with SOPS:

```sh
sops -e --input-type=json config.json > config.json.encrypted
sops -e --input-type=yaml config.yaml > config.yaml.encrypted
sops -e --input-type=env config.env > config.env.encrypted
```

For kustomize-controller to be able to decrypt a JSON config, you need to set
the file extension to `.json`:

```yaml
kind: Kustomization
secretGenerator:
  - name: config
    files:
      - config.json=config.json.encrypted
```

For dotenv files, use the `envs` directive:

```yaml
kind: Kustomization
secretGenerator:
  - name: config
    envs:
      - config.env.encrypted
```

For Docker config files, you need to specify both input and output type as JSON:

```sh
sops -e --input-type=json --output-type=json ghcr.dockerconfigjson > ghcr.dockerconfigjson.encrypted
```

To generate an image pull secret, use the `.dockerconfigjson` as the secret key:

```yaml
kind: Kustomization
secretGenerator:
  - name: ghcr-auth
    type: kubernetes.io/dockerconfigjson
    files:
      - .dockerconfigjson=ghcr.dockerconfigjson.encrypted
```

### Post build substitution of numbers and booleans

When using [variable substitution](#post-build-variable-substitution) with values
that are numbers or booleans, the reconciliation may fail if the substitution
is for a field that must be of type string. To convert the number or boolean
to a string, you can wrap the variable with a double quotes var:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: app
  annotations:
    id: ${quote}${id}${quote}
    enabled: ${quote}${enabled}${quote}
```

Then in the Flux Kustomization, define the variables as:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: app
spec:
  postBuild:
    substitute:
      quote: '"' # double quote var
      id: "123"
      enabled: "true"
```

### Triggering a reconcile

To manually tell the kustomize-controller to reconcile a Kustomization outside
the [specified interval window](#interval), it can be annotated with
`reconcile.fluxcd.io/requestedAt: <arbitrary value>`. Annotating the resource
queues the Kustomization for reconciliation if the `<arbitrary-value>` differs
from the last value the controller acted on, as reported in
[`.status.lastHandledReconcileAt`](#last-handled-reconcile-at).

Using `kubectl`:

```sh
kubectl annotate --field-manager=flux-client-side-apply --overwrite kustomization/<kustomization-name> reconcile.fluxcd.io/requestedAt="$(date +%s)"
```

Using `flux`:

```sh
flux reconcile kustomization <kustomization-name>
```

### Waiting for `Ready`

When a change is applied, it is possible to wait for the Kustomization to reach
a `Ready` state using `kubectl`:

```sh
kubectl wait kustomization/<kustomization-name> --for=condition=ready --timeout=1m
```

### Suspending and resuming

When you find yourself in a situation where you temporarily want to pause the
reconciliation of a Kustomization, you can suspend it using [`.spec.suspend`](#suspend).

To pause the reconciliation of a specific Kubernetes resource managed by a Flux Kustomization,
you can annotate or label the resource in-cluster with:

```yaml
kustomize.toolkit.fluxcd.io/reconcile: disabled
```

**Note:** When the `kustomize.toolkit.fluxcd.io/reconcile` annotation is set to
`disabled`, the controller will no longer apply changes, nor
will it prune the resource. To resume reconciliation, set the annotation to
`enabled` in the source or remove it from the in-cluster object.

#### Suspend a Kustomization

In your YAML declaration:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: <kustomization-name>
spec:
  suspend: true
```

Using `kubectl`:

```sh
kubectl patch kustomization <kustomization-name> --field-manager=flux-client-side-apply -p '{\"spec\": {\"suspend\" : true }}'
```

Using `flux`:

```sh
flux suspend kustomization <kustomization-name>
```

#### Resume a Kustomization

In your YAML declaration, comment out (or remove) the field:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: <kustomization-name>
spec:
  # suspend: true
```

**Note:** Setting the field value to `false` has the same effect as removing
it, but does not allow for "hot patching" using e.g. `kubectl` while practicing
GitOps; as the manually applied patch would be overwritten by the declared
state in Git.

Using `kubectl`:

```sh
kubectl patch kustomization <kustomization-name> --field-manager=flux-client-side-apply -p '{\"spec\" : {\"suspend\" : false }}'
```

Using `flux`:

```sh
flux resume kustomization <kustomization-name>
```

### Debugging a Kustomization

There are several ways to gather information about a Kustomization for
debugging purposes.

#### Describe the Kustomization

Describing a Kustomization using
`kubectl describe kustomization <kustomization-name>`
displays the latest recorded information for the resource in the `Status` and
`Events` sections:

```console
...
Status:
...
  Conditions:
    Last Transition Time:  2023-03-29T06:09:32Z
    Message:               Fetching manifests for revision master/67e2c98a60dc92283531412a9e604dd4bae005a9 with a timeout of 4m30s
    Observed Generation:   3
    Reason:                ProgressingWithRetry
    Status:                True
    Type:                  Reconciling
    Last Transition Time:  2023-03-29T06:09:32Z
    Message:               kustomization path not found: stat /tmp/kustomization-1464362706/invalid: no such file or directory
    Observed Generation:   3
    Reason:                ArtifactFailed
    Status:                False
    Type:                  Ready
  Last Applied Revision:    master/67e2c98a60dc92283531412a9e604dd4bae005a9
  Last Attempted Revision:  master/67e2c98a60dc92283531412a9e604dd4bae005a9
  Observed Generation:      2
Events:
  Type     Reason          Age   From                  Message
  ----     ------          ----  ----                  -------
  Warning  ArtifactFailed           2s   kustomize-controller  kustomization path not found: stat /tmp/kustomization-1464362706/invalid: no such file or directory

```

#### Trace emitted Events

To view events for specific Kustomization(s), `kubectl events` can be used
to list the Events for specific objects. For example, running

```sh
kubectl events -n flux-system --for kustomization/podinfo
```

lists

```console
LAST SEEN   TYPE     REASON                    OBJECT                  MESSAGE
31s         Warning  ArtifactFailed            kustomization/podinfo   kustomization path not found: stat /tmp/kustomization-3011588360/invalid: no such file or directory
26s         Normal   ArtifactFailed            kustomization/podinfo   HorizontalPodAutoscaler/default/podinfo deleted...
18s         Warning  ArtifactFailed            kustomization/podinfo   kustomization path not found: stat /tmp/kustomization-3336282420/invalid: no such file or directory
9s          Normal   Progressing               kustomization/podinfo   Service/default/podinfo created...
9s          Normal   ReconciliationSucceeded   kustomization/podinfo   Reconciliation finished in 75.190237ms, next run in 5m0s
```

You can also use the `flux events` command to view all events for a
Kustomization and its related Source. For example,

```sh
flux events --for Kustomization/podinfo
```

will list all events for the `podinfo` Kustomization in the `flux-system`
namespace and its related Source object, the `podinfo` GitRepository.

```console
LAST SEEN               TYPE    REASON                          OBJECT                  MESSAGE
3m2s                    Warning ArtifactFailed                  Kustomization/podinfo   kustomization path not found: stat /tmp/kustomization-3336282420/invalid: no such file or directory

2m53s                   Normal  ReconciliationSucceeded         Kustomization/podinfo   Reconciliation finished in 75.190237ms, next run in 5m0s

2m53s                   Normal  Progressing                     Kustomization/podinfo   Service/default/podinfo created
                                                                                        Deployment/default/podinfo created
                                                                                        HorizontalPodAutoscaler/default/podinfo created

19s (x17 over 8m24s)    Normal  GitOperationSucceeded           GitRepository/podinfo   no changes since last reconcilation: observed revision 'master/67e2c98a60dc92283531412a9e604dd4bae005a9'
```

Besides being reported in Events, the reconciliation errors are also logged by
the controller. The Flux CLI offer commands for filtering the logs for a
specific Kustomization, e.g.
`flux logs --level=error --kind=Kustomization --name=<kustomization-name>`.

### Reacting immediately to configuration dependencies

To trigger a reconciliation when changes occur in referenced
Secrets or ConfigMaps, you can set the following label on the
Secret or ConfigMap:

```yaml
metadata:
  labels:
    reconcile.fluxcd.io/watch: Enabled
```

An alternative to labeling every Secret or ConfigMap is
setting the `--watch-configs-label-selector=owner!=helm`
[flag](https://fluxcd.io/flux/components/kustomize/options/#flags)
in kustomize-controller, which allows watching all Secrets and
ConfigMaps except for Helm storage Secrets.

**Note**: A reconciliation will be triggered for an event on a
referenced Secret/ConfigMap even if it's marked as optional in
the `.spec.postBuild.substituteFrom` field, including deletion
events.

## Kustomization Status

### Conditions

A Kustomization enters various states during its lifecycle, reflected as
[Kubernetes Conditions][typical-status-properties].
It can be [reconciling](#reconciling-kustomization) while applying the Kustomization on the cluster, it can be [ready](#ready-kustomization), or it can [fail during
reconciliation](#failed-kustomization).

The Kustomization API is compatible with the [kstatus specification][kstatus-spec],
and reports `Reconciling` and `Stalled` conditions where applicable to
provide better (timeout) support to solutions polling the Kustomization to
become `Ready`.

#### Reconciling Kustomization

The kustomize-controller marks a Kustomization as _reconciling_ when it starts
the reconciliation of the same. The Condition added to the Kustomization's
`.status.conditions` has the following attributes:

- `type: Reconciling`
- `status: "True"`
- `reason: Progressing` | `reason: ProgressingWithRetry`

The Condition `message` is updated during the course of the reconciliation to
report the action being performed at any particular moment such as
building manifests, detecting drift, etc.

The `Ready` Condition's `status` is also marked as `Unkown`.

#### Ready Kustomization

The kustomize-controller marks a Kustomization as _ready_ when a Kustomization
is successfully reconciled, i.e. the source was fetched, the kustomization was
built and applied on the cluster and all health checks are observed to be passing.

When the Kustomization is "ready", the controller sets a Condition with the
following attributes in the Kustomization’s `.status.conditions`:

- `type: Ready`
- `status: "True"`
- `reason: ReconciliationSucceeded`

#### Failed Kustomization

The kustomize-controller may get stuck trying to reconcile and apply a
Kustomization without completing. This can occur due to some of the following factors:

- The Source object does not exist on the cluster.
- The Source has not produced an Artifact yet.
- The Kustomization's dependencies aren't ready yet.
- The specified path does not exist in the Artifact.
- Building the kustomization fails.
- Garbage collection fails.
- Running a health check failed.

When this happens, the controller sets the `Ready` Condition status to False
and adds a Condition with the following attributes to the Kustomization’s
`.status.conditions`:

- `type: Ready | HealthyCondition`
- `status: "False"`
- `reason: PruneFailed | ArtifactFailed | BuildFailed | HealthCheckFailed | DependencyNotReady | ReconciliationFailed `

The `message` field of the Condition will contain more information about why
the reconciliation failed.

While the Kustomization has one or more of these Conditions, the controller
will continue to attempt a reconciliation of the Kustomization with an
exponential backoff, until it succeeds and the Kustomization marked as [ready](#ready-kustomization).

Note that a Kustomization can be [reconciling](#reconciling-kustomization)
while failing at the same time, for example, due to a newly introduced
configuration issue in the Kustomization spec. When a reconciliation fails, the
`Reconciling` Condition `reason` would be `ProgressingWithRetry`. When the
reconciliation is performed again after the failure, the `reason` is updated to `Progressing`.

### History

The kustomize-controller maintains a history of the last 5 reconciliations
in `.status.history`, including the digest of the applied manifests, the
source and origin revision, the timestamps and the duration of the reconciliations,
the status and the total number of times a specific digest was reconciled.

```yaml
status:
  history:
    - digest: sha256:43ad78c94b2655429d84f21488f29d7cca9cd45b7f54d2b27e16bbec8eff9228
      firstReconciled: "2025-08-15T10:11:00Z"
      lastReconciled: "2025-08-15T11:12:00Z"
      lastReconciledDuration: 2.818583s
      lastReconciledStatus: ReconciliationSucceeded
      totalReconciliations: 2
      metadata:
        revision: "v1.0.1@sha1:450796ddb2ab6724ee1cc32a4be56da032d1cca0"
    - digest: sha256:ec8dbfe61777b65001190260cf873ffe454451bd2e464bd6f9a154cffcdcd7e5
      firstReconciled: "2025-07-14T13:10:00Z"
      lastReconciled: "2025-08-15T10:00:00Z"
      lastReconciledDuration: 49.813292s
      lastReconciledStatus: HealthCheckFailed
      totalReconciliations: 120
      metadata:
        revision: "v1.0.0@sha1:67e2c98a60dc92283531412a9e604dd4bae005a9"
```

The kustomize-controller deduplicates entries based on the digest and status, with the
most recent reconciliation being the first entry in the list.

### Inventory

In order to perform operations such as drift detection, garbage collection, etc.
kustomize-controller needs to keep track of all Kubernetes objects that are
reconciled as part of a Kustomization. To do this, it maintains an inventory
containing the list of Kubernetes resource object references that have been
successfully applied and records it in `.status.inventory`. The inventory
records are in the format `<namespace>_<name>_<group>_<kind>_<version>`.

```console
Status:
  Inventory:
    Entries:
      Id: default_podinfo__Service
      V:  v1
      Id: default_podinfo_apps_Deployment
      V:  v1
      Id: default_podinfo_autoscaling_HorizontalPodAutoscaler
      V:  v2
```

### Last applied revision

`.status.lastAppliedRevision` is the last revision of the Artifact from the
referred Source object that was successfully applied to the cluster.

### Last applied origin revision

`status.lastAppliedOriginRevision` is the last origin revision of the Artifact
from the referred Source object that was successfully applied to the cluster.

This field is usually retrieved from the Metadata of the Artifact and depends
on the Source type. For example, for OCI artifacts this is the value associated
with the standard metadata key `org.opencontainers.image.revision`, which is
used to track the revision of the source code that was used to build the OCI
artifact.

The controller will forward this value when emitting events in the metadata
key `originRevision`. The notification-controller will look for this key in
the event metadata when sending *commit status update* events to Git providers.

### Last attempted revision

`.status.lastAttemptedRevision` is the last revision of the Artifact from the
referred Source object that was attempted to be applied to the cluster.

### Observed Generation

The kustomize-controller reports an [observed generation][typical-status-properties]
in the Kustomization's `.status.observedGeneration`. The observed generation is
the latest `.metadata.generation` which resulted in either a [ready state](#ready-kustomization),
or stalled due to an error it can not recover from without human
intervention.

### Last Handled Reconcile At

The kustomize-controller reports the last `reconcile.fluxcd.io/requestedAt`
annotation value it acted on in the `.status.lastHandledReconcileAt` field.

For practical information about this field, see [triggering a reconcile](#triggering-a-reconcile).

[typical-status-properties]: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties
[kstatus-spec]: https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus
