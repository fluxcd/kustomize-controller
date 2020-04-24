# Kustomization

The `Kustomization` API defines a pipeline for fetching, building, testing and applying Kubernetes manifests.

## Specification

A **kustomization** object defines the source of Kubernetes manifests by referencing an object 
managed by [source-controller](https://github.com/fluxcd/source-controller),
the path to the kustomization file within that source,
and the interval at which the kustomize build output is applied on the cluster.

```go
type KustomizationSpec struct {
	// A list of kustomization that must be ready before this
	// kustomization can be applied.
	// +optional
	DependsOn []string `json:"dependsOn,omitempty"`

	// When enabled, the kustomization.yaml is automatically generated
	// for all the Kubernetes manifests in the specified path and sub-directories.
	// The generated kustomization.yaml contains a label transformer matching the prune field.
	// +optional
	Generate bool `json:"generate,omitempty"`

	// The interval at which to apply the kustomization.
	// +required
	Interval metav1.Duration `json:"interval"`

	// Path to the directory containing the kustomization file.
	// +kubebuilder:validation:Pattern="^\\./"
	// +required
	Path string `json:"path"`

	// Label selector used for garbage collection.
	// +kubebuilder:validation:Pattern="^.*=.*$"
	// +optional
	Prune string `json:"prune,omitempty"`

	// A list of workloads (Deployments, DaemonSets and StatefulSets)
	// to be included in the health assessment.
	// +optional
	HealthChecks []WorkloadReference `json:"healthChecks,omitempty"`

	// Reference of the source where the kustomization file is.
	// +required
	SourceRef corev1.TypedLocalObjectReference `json:"sourceRef"`

	// This flag tells the controller to suspend subsequent kustomize executions,
	// it does not apply to already started executions. Defaults to false.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Timeout for validation, apply and health checking operations.
	// Defaults to 'Interval' duration.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Validate the Kubernetes objects before applying them on the cluster.
	// The validation strategy can be 'client' (local dry-run) or 'server' (APIServer dry-run).
	// +kubebuilder:validation:Enum=client;server
	// +optional
	Validation string `json:"validation,omitempty"`
}
```

The status sub-resource describes the result of the last kustomization execution:

```go
type KustomizationStatus struct {
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`

	// The last successfully applied revision.
	// The revision format for Git sources is <branch|tag>/<commit-sha>.
	// +optional
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`
}
```

Status condition types:

```go
const (
	// ReadyCondition represents the fact that a given kustomization has passed
	// validation and was successfully applied on the cluster.
	ReadyCondition string = "Ready"
)
```

Status condition reasons:

```go
const (
	// ApplySucceedReason represents the fact that the kustomization apply succeed.
	ApplySucceedReason string = "ApplySucceed"

	// ApplyFailedReason represents the fact that the kustomization apply failed.
	ApplyFailedReason string = "ApplyFailed"

	// BuildFailedReason represents the fact that the kustomize build command failed.
	BuildFailedReason string = "BuildFailed"

	// SuspendedReason represents the fact that the kustomization execution is suspended.
	SuspendedReason string = "Suspended"

	// ValidationFailedReason represents the fact that the dry-run apply failed.
	ValidationFailedReason string = "ValidationFailed"

	// ArtifactFailedReason represents the fact that the artifact acquisition failed.
	ArtifactFailedReason string = "ArtifactFailed"
)
```

## Source reference

The kustomization `spec.sourceRef` is a reference to an object managed by
[source-controller](https://github.com/fluxcd/source-controller). When the source
[revision](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/common.md#source-status) 
changes, it generates a Kubernetes event that triggers a kustomize build and apply.

Source supported types:

* [GitRepository](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/gitrepositories.md)

> **Note** that the source should contain the kustomization.yaml and all the
> Kubernetes manifests and configuration files referenced in the kustomization.yaml.
> If your repository contains only plain manifests, then you should enable kustomization.yaml generation.

## Generate kustomization.yaml

If your repository contains plain Kubernetes manifests, you can configure the
controller to generate a `kustomization.yaml` by setting `spec.generate` to `true`.

When `spec.generate` is enabled, the `kustomization.yaml` file is automatically generated for
all the Kubernetes manifests in the `spec.path` and sub-directories.

If the `spec.prune` is not empty, the controller generates a label transformer to enable
[garbage collection](#garbage-collection).

## Reconciliation

The kustomization `spec.interval` tells the controller at which interval to fetch the
Kubernetes manifest for the source, build the kustomization and apply it on the cluster.
The interval time units are `s`, `m` and `h` e.g. `interval: 5m`, the minimum value should be over 60 seconds.

The kustomization execution can be suspended by setting `spec.susped` to `true`.

The controller can be told to execute the kustomization outside of the specified interval
by annotating the kustomization object with:

```go
const (
	// SyncAtAnnotation is the annotation used for triggering a
	// sync outside of the specified schedule.
	SyncAtAnnotation string = "kustomize.fluxcd.io/syncAt"
)
```

On-demand execution example:

```bash
kubectl annotate --overwrite kustomization/podinfo kustomize.fluxcd.io/syncAt="$(date +%s)"
```

## Garbage collection

Garbage collection means that the Kubernetes objects that were previously applied on the cluster
but are missing from the current apply, are removed from cluster automatically.
Garbage collection is also performed when a Kustomization object is deleted,
triggering a removal of all Kubernetes objects previously applied on the cluster.

Tpo enable garbage collection, all Kubernetes objects must have common labels matching the `spec.prune`
[label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/).

For example, `prune: env=dev,app=frontend` requires a `kustomization.yaml` with `commonLabels`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
commonLabels:
  env: dev
  app: frontend
```

> **Note** that each kustomization must have a unique combination of label key/values, otherwise the 
> garbage collection will remove resources outside of the kustomization scope.

Another option to label all Kubernetes objects, is with label transformers:

```yaml
apiVersion: builtin
kind: LabelTransformer
metadata:
  name: labels
labels:
  env: dev
  app: frontend
fieldSpecs:
  - path: metadata/labels
    create: true
```

Save the above file as `labels.yaml` and add it to your `kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
transformers:
  - labels.yaml
```

## Health assessment

A kustomization can contain a series of health checks used to determine the
[rollout status](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status)
of the deployed workloads. A health check entry can reference one of the following Kubernetes types:
Deployment, DaemonSet or StatefulSet.

Assuming the kustomization source contains a Kubernetes Deployment named `backend`,
a health check can be defined as follows:

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: backend
spec:
  interval: 5m
  path: "./webapp/backend/"
  prune: "component=backend"
  sourceRef:
    kind: GitRepository
    name: webapp
  healthChecks:
    - kind: Deployment
      name: backend
      namespace: dev
  timeout: 2m
```

After applying the kustomize build output, the controller verifies if the rollout completed successfully.
If the deployment was successful, the kustomization ready condition is marked as `true`,
if the rollout failed, or if it takes more than the specified timeout to complete, then the 
kustomization ready condition is set to `false`. If the deployment becomes healthy on the next
execution, then the kustomization is marked as ready.

## Kustomization dependencies 

When applying a kustomization, you may need to make sure other resources exist before the
workloads defined in your kustomization are deployed.
For example, a namespace must exist before applying resources to it.

With `spec.dependsOn` you can specify that the execution of a kustomization follows another.
When you add `dependsOn` entries to a kustomization, that kustomization is applied
only after all of its dependencies are ready. The readiness state of a kustomization is determined by 
its last apply status condition.

Assuming two kustomizations:
* `common` - contains a namespace and service accounts definitions
* `backend` - contains the workloads to be deployed in that namespace

You can instruct the controller to apply the `common` kustomization before `backend`:

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: common
spec:
  interval: 5m
  path: "./webapp/common/"
  prune: "part-of=webapp"
  sourceRef:
    kind: GitRepository
    name: webapp
---
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: backend
spec:
  dependsOn:
    - common
  interval: 5m
  path: "./webapp/backend/"
  prune: "part-of=webapp,component=backend"
  sourceRef:
    kind: GitRepository
    name: webapp
```

When combined with health assessment, a kustomization will run after all its dependencies health checks are passing.
For example, a service mesh proxy injector should be running before deploying applications inside the mesh.

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: istio
spec:
  interval: 5m
  path: "./profiles/default/"
  sourceRef:
    kind: GitRepository
    name: istio
  healthChecks:
    - kind: Deployment
      name: istiod
      namespace: istio-system
  timeout: 2m
---
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: backend
spec:
  dependsOn:
    - common
    - istio
  interval: 5m
  path: "./webapp/backend/"
  prune: "part-of=webapp,component=backend"
  sourceRef:
    kind: GitRepository
    name: webapp
```

> **Note** that circular dependencies between kustomizations must be avoided, otherwise the
> interdependent kustomizations will never be applied on the cluster.

## Status

When the controller completes a kustomization apply, reports the result in the `status` sub-resource.

A successful reconciliation sets the ready condition to `true` and updates the revision field:

```yaml
status:
  conditions:
  - lastTransitionTime: "2020-04-23T19:28:48Z"
    message: kustomization was successfully applied
    reason: ApplySucceed
    status: "True"
    type: Ready
  lastAppliedRevision: master/a1afe267b54f38b46b487f6e938a6fd508278c07
```

You can wait for the kustomize controller to complete a reconciliation with:

```bash
kubectl wait kustomization/backend --for=condition=ready
```

The controller logs the Kubernetes objects:

```json
{
  "level": "info",
  "ts": 1587195448.071468,
  "logger": "controllers.Kustomization",
  "msg": "Kustomization applied in 1.436096591s",
  "kustomization": "default/backend",
  "output": {
    "service/backend": "created",
    "deployment.apps/backend": "created",
    "horizontalpodautoscaler.autoscaling/backend": "created"
  }
}
```

A failed reconciliation sets the ready condition to `false`:

```yaml
status:
  conditions:
  - lastTransitionTime: "2020-04-23T19:29:48Z"
    message: 'server-side validation failed'
    reason: ValidationFailed
    status: "False"
    type: Ready
  lastAppliedRevision: master/a1afe267b54f38b46b487f6e938a6fd508278c07
``` 

> **Note** that the last applied revision is updated only on a successful reconciliation.

When a reconciliation fails, the controller logs the error:

```json
{
  "level": "error",
  "ts": 1587195448.071468,
  "logger": "controllers.Kustomization",
  "msg": "server-side validation failed",
  "kustomization": "default/backend",
  "error": "The Service 'backend' is invalid: spec.type: Unsupported value: 'Ingress'"
}
```

