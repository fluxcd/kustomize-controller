# Kustomization

The `Kustomization` API defines a pipeline for fetching, decrypting, building,
validating and applying Kustomize overlays or plain Kubernetes manifests.
The `Kustomization` Custom Resource Definition is the
counterpart of Kustomize' `kustomization.yaml` config file.

**Note:** Do not confuse Kustomization with `apiVersion: source.toolkit.fluxcd.io/v1` (defined by Flux) from the
one with `apiVersion: kustomize.config.k8s.io/v1beta1` (defined by Kustomize).
See the related [faq entry](https://fluxcd.io/flux/faq/#are-there-two-kustomization-types).

## Example

The following is an example of a Flux Kustomization that reconciles on the
cluster the Kubernetes manifests stored in a Git repository.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1beta2
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
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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
```

In the above example:

- A Flux GitRepository named `podinfo` is created that clones the `master` branch
  and makes the repository content available as an Artifact inside the cluster.
- A Flux Kustomization named `podinfo` is created that watches the GitRepository for artifact changes.
- The Kustomization builds the YAML manifests located at the specified `spec.path`,
  sets the namespace of all objects to the `spec.targetNamespace`,
  validates the objects against the Kubernetes API, and finally applies them on the cluster. 
- Every ten minutes, the Kustomization runs a server-side apply dry-run to detect and correct drift inside the cluster.
- When the Git revision changes, the manifests are reconciled automatically. If previously applied objects
  are missing from the current revision, these objects are deleted from the cluster when `spec.prune` is enabled.

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

4. Run `kubectl describe kustomization podinfo` to see the reconciliation status conditions and events:

   ```console
   ...
    Status:
      Conditions:
        Last Transition Time:  2022-06-07T11:14:41Z
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

## Recommended settings

When deploying applications to production environments,
it is recommended to configure the following fields,
while adjusting them to your desires for responsiveness:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1beta2
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
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

### Disable Kustomize remote bases

For security and performance reasons, it is advised to disallow the usage of
[remote bases](https://github.com/kubernetes-sigs/kustomize/blob/a7f4db7fb41e17b2c826a524f545e6174b4dc6ac/examples/remoteBuild.md)
in Kustomize overlays. To enforce this setting, platform admins can use the `--no-remote-bases=true` controller flag.

## Source reference

The Kustomization `spec.sourceRef` is a reference to an object managed by
[source-controller](https://github.com/fluxcd/source-controller). When the source
[revision](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1beta1/common.md#source-status)
changes, it generates a Kubernetes event that triggers a kustomize build and apply.

Source supported types:

- [GitRepository](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1beta2/gitrepositories.md)
- [OCIRepository](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1beta2/ocirepositories.md)
- [Bucket](https://github.com/fluxcd/source-controller/blob/main/docs/spec/v1beta2/buckets.md)

**Note:** If the source contains a `kustomization.yaml` file, then it should also contain
all the Kubernetes manifests and configuration files referenced in the Kustomize config file.
If your Git, OCI repository or S3 bucket contains **plain manifests**,
then a kustomization.yaml will be [automatically generated](#generate-kustomizationyaml)
by the controller.

### Cross-namespace references

A Kustomization can refer to a source from a different namespace with `spec.sourceRef.namespace` e.g.:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

On multi-tenant clusters, platform admins can disable cross-namespace references with the
`--no-cross-namespace-refs=true` flag.

## Generate kustomization.yaml

If your repository contains plain Kubernetes manifests, the
`kustomization.yaml` file is automatically generated for all the Kubernetes
manifests in the directory tree specified in the `spec.path` field of the Flux `Kustomization`.
All YAML files present under that path must be valid Kubernetes
manifests, unless they're excluded either by way of the `.sourceignore`
file or the `spec.ignore` field on the corresponding source object.

Example of excluding CI workflows and SOPS config files:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1beta2
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

It is recommended to generate the `kustomization.yaml` on your own and store it in Git, this way you can
validate your manifests in CI (example script [here](https://github.com/fluxcd/flux2-multi-tenancy/blob/main/scripts/validate.sh)).
Assuming your manifests are inside `apps/my-app`, you can generate a `kustomization.yaml` with:

```sh
cd apps/my-app

# create kustomization.yaml
kustomize create --autodetect --recursive
```

## Reconciliation

The Kustomization `spec.interval` tells the controller at which interval to fetch the
Kubernetes manifest for the source, build the Kustomization and apply it on the cluster.
The interval time units are `s` and `m` e.g. `interval: 5m`, the minimum value should be over 60 seconds.

The Kustomization execution can be suspended by setting `spec.suspend` to `true`.

The controller can be told to reconcile the Kustomization outside of the specified interval
by annotating the Kustomization object with:

```yaml
reconcile.fluxcd.io/requestedAt: "2022-03-02T13:59:52.758922834Z"
```

On-demand execution example:

```sh
kubectl annotate --field-manager=flux-client-side-apply --overwrite \
kustomization/podinfo reconcile.fluxcd.io/requestedAt="$(date +%s)"
```

List all Kubernetes objects reconciled from a Kustomization:

```sh
kubectl get all --all-namespaces \
-l=kustomize.toolkit.fluxcd.io/name="<Kustomization name>" \
-l=kustomize.toolkit.fluxcd.io/namespace="<Kustomization namespace>"
```

With `spec.force` you can tell the controller to replace the resources in-cluster if the
patching fails due to immutable fields changes.

You can enable force apply for specific resources by labeling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/force: enabled
```

You can configure the controller to ignore in-cluster resources by labeling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/reconcile: disabled
```

**Note:** When the `kustomize.toolkit.fluxcd.io/reconcile` annotation is set to `disabled`,
the controller will no longer apply changes from source, nor will it prune the resource.
To resume reconciliation, set the annotation to `enabled` in source
and remove it from the in-cluster object.

If you use kubectl to edit an object managed by Flux, all changes will be undone when
the controller reconciles a Flux Kustomization containing that object.
In order to preserve fields added with kubectl, you have to specify a field manager
named `flux-client-side-apply` e.g.:

```sh
kubectl apply --field-manager=flux-client-side-apply
```

Another option is to annotate or label objects with:

```yaml
kustomize.toolkit.fluxcd.io/ssa: merge
```

**Note:** The fields defined in manifests will always be overridden,
the above procedure works only for adding new fields that don’t overlap with the desired state.

For lists fields which are atomic (e.g `spec.tolerations` in PodSpec), Kubernetes doesn't allow different managers 
for such fields, therefore any changes to these fields will be undone, even if you specify a manager. 
For more context, please see the Kubernetes enhancement doc:
[555-server-side-apply](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/555-server-side-apply/README.md#lists).

## Garbage collection

To enable garbage collection, set `spec.prune` to `true`.

Garbage collection means that the Kubernetes objects that were previously applied on the cluster
but are missing from the current source revision, are removed from cluster automatically.
Garbage collection is also performed when a Kustomization object is deleted,
triggering a removal of all Kubernetes objects previously applied on the cluster.

To keep track of the Kubernetes objects reconciled from a Kustomization, the controller
creates an inventory of the last applied resources. The inventory records are in the
format `<namespace>_<name>_<group>_<kind>_<version>` and they are stored in-cluster
under `.status.inventory.entries`.

You can disable pruning for certain resources by either
labeling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/prune: disabled
```

## Health assessment

A Kustomization can contain a series of health checks used to determine the
[rollout status](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status)
of the deployed workloads and the ready status of custom resources.

To enabled health checking for all the reconciled resources,
set `spec.wait` and `spec.timeout`:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: backend
  namespace: default
spec:
  interval: 5m
  path: "./deploy"
  sourceRef:
    kind: GitRepository
    name: webapp
  wait: true
  timeout: 2m
```

If you wish to select only certain resources, list them under `spec.healthChecks`.

**Note:** When `spec.wait` is enabled, the `spec.healthChecks` field is ignored.

A health check entry can reference one of the following types:

- Kubernetes builtin kinds: Deployment, DaemonSet, StatefulSet, PersistentVolumeClaim, Pod, PodDisruptionBudget, Job, CronJob, Service, Secret, ConfigMap, CustomResourceDefinition
- Toolkit kinds: HelmRelease, HelmRepository, GitRepository, etc
- Custom resources that are compatible with [kstatus](https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus)

Assuming the Kustomization source contains a Kubernetes Deployment named `backend`,
a health check can be defined as follows:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: backend
  namespace: default
spec:
  interval: 5m
  path: "./webapp/backend/"
  prune: true
  sourceRef:
    kind: GitRepository
    name: webapp
  healthChecks:
    - apiVersion: apps/v1
      kind: Deployment
      name: backend
      namespace: dev
  timeout: 2m
```

After applying the kustomize build output, the controller verifies if the rollout completed successfully.
If the deployment was successful, the Kustomization ready condition is marked as `true`,
if the rollout failed, or if it takes more than the specified timeout to complete, then the
Kustomization ready condition is set to `false`. If the deployment becomes healthy on the next
execution, then the Kustomization is marked as ready.

When a Kustomization contains HelmRelease objects, instead of checking the underling Deployments, you can
define a health check that waits for the HelmReleases to be reconciled with:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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
    - apiVersion: helm.toolkit.fluxcd.io/v2beta1
      kind: HelmRelease
      name: frontend
      namespace: dev
    - apiVersion: helm.toolkit.fluxcd.io/v2beta1
      kind: HelmRelease
      name: backend
      namespace: dev
  timeout: 5m
```

If all the HelmRelease objects are successfully installed or upgraded, then the Kustomization will be marked as ready.

## Kustomization dependencies

When applying a Kustomization, you may need to make sure other resources exist before the
workloads defined in your Kustomization are deployed.
For example, a namespace must exist before applying resources to it.

With `spec.dependsOn` you can specify that the execution of a Kustomization follows another.
When you add `dependsOn` entries to a Kustomization, that Kustomization is applied
only after all of its dependencies are ready. The readiness state of a Kustomization is determined by
its last apply status condition.

Assuming two Kustomizations:

- `cert-manager` - reconciles the cert-manager CRDs and controller
- `certs` - reconciles the cert-manager custom resources

You can instruct the controller to apply the `cert-manager` Kustomization before `certs`:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

When combined with health assessment, a Kustomization will run after all its dependencies health checks are passing.
For example, a service mesh proxy injector should be running before deploying applications inside the mesh.

**Note:** Circular dependencies between Kustomizations must be avoided, otherwise the
interdependent Kustomizations will never be applied on the cluster.

## Role-based access control

By default, a Kustomization apply runs under the cluster admin account and can create, modify, delete
cluster level objects (namespaces, CRDs, etc) and namespaced objects (deployments, ingresses, etc).
For certain Kustomizations a cluster admin may wish to control what types of Kubernetes objects can
be reconciled and under which namespaces.
To restrict a Kustomization, one can assign a service account under which the reconciliation is performed.

Assuming you want to restrict a group of Kustomizations to a single namespace, you can create an account
with a role binding that grants access only to that namespace:

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
are running under that service account should depend-on the one that contains the account.

Create a Kustomization that prevents altering the cluster state outside of the `webapp` namespace:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: backend
  namespace: webapp
spec:
  serviceAccountName: flux
  dependsOn:
    - name: common
  interval: 5m
  path: "./webapp/backend/"
  prune: true
  sourceRef:
    kind: GitRepository
    name: webapp
```

When the controller reconciles the `frontend-webapp` Kustomization, it will impersonate the `flux`
account. If the Kustomization contains cluster level objects like CRDs or objects belonging to a different
namespace, the reconciliation will fail since the account it runs under has no permissions to alter objects
outside of the `webapp` namespace.

### Enforce impersonation

On multi-tenant clusters, platform admins can enforce impersonation with the
`--default-service-account` flag.

When the flag is set, all Kustomizations which don't have `spec.serviceAccountName` specified
will use the service account name provided by `--default-service-account=<SA Name>`
in the namespace of the object.

## Override kustomize config

The Kustomization has a set of fields to extend and/or override the Kustomize
patches and namespace on all the Kubernetes objects reconciled by the resource,
offering support for the following Kustomize directives:

- [namespace](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/namespace/)
- common [labels](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonlabels/) and [annotations](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonannotations/)
- [patches](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/patches/)
- [images](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/images/)
- [components](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/components/)

### Target namespace

To configure the [Kustomize `namespace`](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/namespace/)
and overwrite the namespace of all the Kubernetes objects reconciled by the `Kustomization`,
`spec.targetNamespace` can be defined:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  # ...omitted for brevity
  targetNamespace: test
```

While the field `targetNamespace` in a Kustomization is optional,
if this field is non-empty then the Kubernetes namespace pointed to by `targetNamespace`
must exist prior to the Kustomization being applied, kustomize-controller will not create the namespace.

### Common Metadata

With `spec.commonMetadata` you can set common labels and annotations to all resources.

The main difference to the Kustomize
[commonLabels](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonlabels/)
and [commonAnnotations](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonannotations/),
is that the controller sets the labels and annotations only to the top level `metadata` field,
without patching the Kubernetes Deployment `spec.template` or the Service `spec.selector`.

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: my-app
  namespace: apps
spec:
  # ...omitted for brevity
  commonMetadata:
    labels:
      app.kubernetes.io/part-of: my-app
    annotations:
      a8r.io/owner: my-team
```

**Note:** Any existing label or annotation will be overridden if its key matches a common one.

### Patches

To add [Kustomize `patches` entries](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/patches/)
to the configuration, and patch resources using either a [strategic merge](https://kubectl.docs.kubernetes.io/references/kustomize/glossary#patchstrategicmerge) 
patch or a [JSON6902](https://kubectl.docs.kubernetes.io/references/kustomize/glossary#patchjson6902) patch,
`spec.patches` items must contain a `target` selector and a `patch` document.
The patch can target a single resource or multiple resources:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

To add [Kustomize `images` entries](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/images/)
to the configuration, and overwrite the name, tag or digest of container images
without creating patches, `spec.images` can be defined:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

To add [Kustomize `components` entries](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/components/)
to the configuration, and use reusable pieces of configuration logic that can
be included from multiple overlays, `spec.components` can be defined:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: podinfo
  namespace: flux-system
spec:
  # ...omitted for brevity
  components:
  - ingress
  - tls
```

**Note:** The component paths must be local and relative to the source root.

**Warning:** Components are an alpha feature in Kustomize and are therefore
considered experimental in Flux. No guarantees are provided as the feature may
be modified in backwards incompatible ways or removed without warning.

## Variable substitution

With `spec.postBuild.substitute` you can provide a map of key/value pairs holding the
variables to be substituted in the final YAML manifest, after kustomize build.

With `spec.postBuild.substituteFrom` you can provide a list of ConfigMaps and Secrets
from which the variables are loaded.
The ConfigMap and Secret data keys are used as the var names.

The `spec.postBuild.substituteFrom.optional` field indicates how the
controller should handle a referenced ConfigMap or Secret being absent
at renconciliation time. The controller's default behavior ― with
`optional` unspecified or set to `false` ― has it fail reconciliation if
the referenced object is missing. By setting the `optional` field to
`true`, you can indicate that controller should use the referenced
object if it's there, but also tolerate its absence, treating that
absence as if the object had been present but empty, defining no
variables.

This offers basic templating for your manifests including support
for [bash string replacement functions](https://github.com/drone/envsubst) e.g.:

- `${var:=default}`
- `${var:position}`
- `${var:position:length}`
- `${var/substring/replacement}`

**Note:** The name of a variable can contain only alphanumeric and underscore characters.
The controller validates the var names using this regular expression:
`^[_[:alpha:]][_[:alpha:][:digit:]]*$`.

Assuming you have manifests with the following variables:

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

You can specify the variables and their values in the Kustomization definition under
`substitute` and/or `substituteFrom` post build section:

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: apps
spec:
  interval: 5m
  path: "./apps/"
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

**Note:** For substituting variables in a secret, `spec.stringData` field must be used i.e

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
take precedence over the ones in `substituteFrom`.

**Note:** If you want to avoid var substitutions in scripts embedded in ConfigMaps or container commands,
you must use the format `$var` instead of `${var}`. If you want to keep the curly braces you can use `$${var}`
which will print out `${var}`. 

All the undefined variables in the format `${var}` will be substituted with string empty, unless a default 
is provided e.g. `${var:=default}`.

You can disable the variable substitution for certain resources by either
labeling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/substitute: disabled
``` 

Substitution of variables only happens if at least a single variable or resource to substitute
from is defined. This may cause issues if you rely on expressions which should evaluate to a
default, even if no other variables are configured. To work around this, one can set an
arbitrary key/value pair to enable the substitution of variables. For example: 

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
kind: Kustomization
metadata:
  name: apps
spec:
  ...
  postBuild:
    substitute:
      var_substitution_enabled: "true"
```

You can replicate the controller post-build substitutions locally using
[kustomize](https://github.com/kubernetes-sigs/kustomize)
and Drone's [envsubst](https://github.com/drone/envsubst):

```console
$ go install github.com/drone/envsubst/cmd/envsubst

$ export cluster_region=eu-central-1
$ kustomize build ./apps/ | $GOPATH/bin/envsubst 
---
apiVersion: v1
kind: Namespace
metadata:
  name: apps
  labels:
    environment: dev
    region: eu-central-1
```

## Remote Clusters / Cluster-API

If the `kubeConfig` field is set, objects will be applied, health-checked, pruned, and deleted for the default
cluster specified in that KubeConfig instead of using the in-cluster ServiceAccount.

The secret defined in the `kubeConfig.SecretRef` must exist in the same namespace as the Kustomization.
On every reconciliation, the KubeConfig bytes will be loaded from the `.secretRef.key`
key (default: `value` or `value.yaml`) of the Secret's data , and the Secret can thus be regularly
updated if cluster-access-tokens have to rotate due to expiration.

This composes well with Cluster API bootstrap providers such as CAPBK (kubeadm), CAPA (AWS) and others.

To reconcile a Kustomization to a CAPI controlled cluster, put the `Kustomization` in the same namespace as your
`Cluster` object, and set the `kubeConfig.secretRef.name` to `<cluster-name>-kubeconfig`:

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
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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

If you wish to target clusters created by other means than CAPI, you can create a ServiceAccount
on the remote cluster, generate a KubeConfig for that account, and then create a secret on the
cluster where kustomize-controller is running e.g.:

```sh
kubectl create secret generic prod-kubeconfig \
    --from-file=value.yaml=./kubeconfig
```

**Note:** The KubeConfig should be self-contained and not rely on binaries, environment,
or credential files from the kustomize-controller Pod.
This matches the constraints of KubeConfigs from current Cluster API providers.
KubeConfigs with `cmd-path` in them likely won't work without a custom,
per-provider installation of kustomize-controller.

When both `spec.kubeConfig` and `spec.ServiceAccountName` are specified,
the controller will impersonate the service account on the target cluster.

## Secrets decryption

In order to store secrets safely in a public or private Git repository,
you can use [Mozilla SOPS](https://github.com/mozilla/sops)
and encrypt your Kubernetes Secrets data with [age](https://age-encryption.org/v1/)
and [OpenPGP](https://www.openpgp.org) keys, or using provider
implementations like Azure Key Vault, GCP KMS or Hashicorp Vault.

**Note:** You should encrypt only the `data` section of the Kubernetes
Secret, encrypting the `metadata`, `kind` or `apiVersion` fields is not
supported. An easy way to do this is by appending
`--encrypted-regex '^(data|stringData)$'` to your `sops --encrypt` command.

### Decryption Secret reference

To configure what keys must be used for decryption, a `.decryption.secretRef`
can be specified with a reference to a Secret in the same namespace as the
Kustomization.

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1beta2
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
    secretRef:
      name: sops-keys
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
  name: sops-keys
  namespace: default
data:
  # Exemplary age private key 
  identity.agekey: <BASE64>
  # Examplary Hashicorp Vault token
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

#### AWS KMS Secret Entry

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
fields must be configured as the `sops.azure-kv` value. It
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

### Controller global decryption

Other than [authentication using a Secret reference](#decryption-secret-reference),
it is possible to specify global decryption settings on the
kustomize-controller Pod. When the controller fails to find credentials on the
Kustomization object itself, it will fall back to these defaults.

#### AWS KMS

While making use of the [IAM OIDC provider](https://eksctl.io/usage/iamserviceaccounts/)
on your EKS cluster, you can create an IAM Role and Service Account with access
to AWS KMS (using at least `kms:Decrypt` and `kms:DescribeKey`). Once these are
created, you can annotate the kustomize-controller Service Account with the
Role ARN, granting the controller permissions to decrypt the Secrets. Please refer
to the [SOPS guide](https://fluxcd.io/flux/guides/mozilla-sops/#aws) for detailed steps.

```sh
kubectl -n flux-system annotate serviceaccount kustomize-controller \
  --field-manager=flux-client-side-apply \
  eks.amazonaws.com/role-arn='arn:aws:iam::<ACCOUNT_ID>:role/<KMS-ROLE-NAME>'
```

Furthermore, you can also use the usual [environment variables used for specifying AWS
credentials](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html#envvars-list)
, by patching the kustomize-controller deployment:

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
        - name: AWS_ACCESS_KEY_ID
          valueFrom:
            secretKeyRef:
              name: aws-creds
              key: awsAccessKeyID
        - name: AWS_SECRET_ACCESS_KEY
          valueFrom:
            secretKeyRef:
              name: aws-creds
              key: awsSecretAccessKey
        - name: AWS_SESSION_TOKEN
          valueFrom:
            secretKeyRef:
              name: aws-creds
              key: awsSessionToken
```

In addition to this, the
[general SOPS documentation around KMS AWS applies](https://github.com/mozilla/sops#27kms-aws-profiles),
allowing you to specify e.g. a `SOPS_KMS_ARN` environment variable.

**Note:**: If you're mounting a secret containing the AWS credentials as a file in the `kustomize-controller` pod,
you'd need to specify an environment variable `$HOME`, since the AWS credentials file is expected to be present
at `~/.aws`, like so:
```yaml
env:
  - name: HOME
    value: /home/{$USER}
```


#### Azure Key Vault

While making use of [AAD Pod Identity](https://github.com/Azure/aad-pod-identity),
you can bind a Managed Identity to Flux's kustomize-controller. Once the
`AzureIdentity` and `AzureIdentityBinding` for this are created, you can patch
the controller's Deployment with the `aadpodidbinding` label set to the
selector of the binding, and the `AZURE_AUTH_METHOD` environment variable set
to `msi`.

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kustomize-controller
  namespace: flux-system
spec:
  template:
    metadata:
      labels:
        aadpodidbinding: sops-akv-decryptor  # match the AzureIdentityBinding selector
    spec:
      containers:
      - name: manager
        env:
        - name: AZURE_AUTH_METHOD
          value: msi
```

In addition to this, the [default SOPS Azure Key Vault flow is
followed](https://github.com/mozilla/sops#encrypting-using-azure-key-vault),
allowing you to specify a variety of other environment variables.

#### GCP KMS

While making use of Google Cloud Platform, the [`GOOGLE_APPLICATION_CREDENTIALS`
environment variable](https://cloud.google.com/docs/authentication/production)
is automatically taken into account.
[Granting permissions](https://cloud.google.com/kms/docs/reference/permissions-and-roles)
to the Service Account attached to this will therefore be sufficient to decrypt
data. When running outside GCP, it is possible to manually patch the
kustomize-controller Deployment with a valid set of (mounted) credentials.

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
        - name: GOOGLE_APPLICATION_CREDENTIALS
          value: /var/gcp/credentials.json
        volumeMounts:
        - name: gcp-credentials
          mountPath: /var/gcp/
          readOnly: true
      volumes:
      - name: gcp-credentials
        secret:
          secretName: mysecret
          items:
          - key: credentials
            path: credentials.json
```

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

### Kustomize secretGenerator

SOPS encrypted data can be stored as a base64 encoded Secret,
which enables the use of Kustomize `secretGenerator` as follows:

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

For secrets in `.json`, `.yaml` `.ini` and `.env` format, make sure you specify the input type when encrypting them with SOPS:

```sh
sops -e --input-type=json config.json > config.json.encrypted
sops -e --input-type=yaml config.yaml > config.yaml.encrypted
sops -e --input-type=env config.env > config.env.encrypted
```

For kustomize-controller to be able to decrypt a JSON config, you need to set the file extension to `.json`:

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

## Status

Every time the controller starts reconciling a `Kustomization`, it adds the `Reconciling` condition in `status` and
updates its message to report the action performed during a reconciliation run:

```yaml
conditions:
- lastTransitionTime: "2022-10-17T13:40:21Z"
  message: Detecting drift for revision main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07 with a timeout of 50s
  observedGeneration: 2
  reason: Progressing
  status: "True"
  type: Reconciling
- lastTransitionTime: "2022-10-17T13:40:21Z"
  message: Reconciliation in progress
  observedGeneration: 2
  reason: Progressing
  status: Unknown
  type: Ready
```

If the reconciliation finishes successfully, the `Reconciling` condition is removed from `status`
and the `Ready` condition is set to `True`:

```yaml
status:
  conditions:
  - lastTransitionTime: "2022-10-17T13:40:21Z"
    message: "Applied revision: main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07"
    reason: ReconciliationSucceeded
    status: "True"
    type: Ready
  lastAppliedRevision: main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07
  lastAttemptedRevision: main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07
```

If `spec.wait` or `spec.healthChecks` is enabled, the health assessment result
is reported under the `Healthy` condition. A failed health check will set both
`Ready` and `Healthy` conditions to `False`.

You can wait for the kustomize controller to complete a reconciliation with:

```bash
kubectl wait kustomization/backend --for=condition=ready
```

The controller logs the Kubernetes objects:

```json
{
  "level": "info",
  "ts": "2022-09-17T07:27:11.921Z",
  "controllerGroup": "kustomize.toolkit.fluxcd.io",
  "msg": "server-side apply completed",
  "name": "backend",
  "namespace": "default",
  "revision": "main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07",
  "output": {
    "service/backend": "created",
    "deployment.apps/backend": "created",
    "horizontalpodautoscaler.autoscaling/backend": "created"
  }
}
```

A failed reconciliation sets the `Ready` condition to `false`:

```yaml
status:
  conditions:
  - lastTransitionTime: "2020-09-17T07:26:48Z"
    message: "The Service 'backend' is invalid: spec.type: Unsupported value: 'Ingress'"
    reason: ValidationFailed
    status: "False"
    type: Ready
  lastAppliedRevision: master@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07
  lastAttemptedRevision: master@sha1:7c500d302e38e7e4a3f327343a8a5c21acaaeb87
```

**Note:** The last applied revision is updated only on a successful reconciliation.

When a reconciliation fails, the controller logs the error and issues a Kubernetes event:

```json
{
  "level": "error",
  "ts": "2022-09-17T07:27:11.921Z",
  "controllerGroup": "kustomize.toolkit.fluxcd.io",
  "msg": "server-side apply completed",
  "name": "backend",
  "namespace": "default",
  "revision": "main@sha1:a1afe267b54f38b46b487f6e938a6fd508278c07",
  "error": "The Service 'backend' is invalid: spec.type: Unsupported value: 'Ingress'"
}
```
