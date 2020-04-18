# Kustomization API

## Specification

A **kustomization** object defines the source of Kubernetes manifests by referencing an object 
managed by [source-controller](https://github.com/fluxcd/source-controller),
the path to the kustomization file within that source,
and the interval at which the kustomization is applied on the cluster.

```go
type KustomizationSpec struct {
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

	// Reference of the source where the kustomization file is.
	// +required
	SourceRef corev1.TypedLocalObjectReference `json:"sourceRef"`

	// This flag tells the controller to suspend subsequent kustomize executions,
	// it does not apply to already started executions. Defaults to false.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Validate the Kubernetes objects before applying them on the cluster.
	// The validation strategy can be 'client' (local dry-run) or 'server' (APIServer dry-run).
	// +kubebuilder:validation:Enum=client;server
	// +optional
	Validation string `json:"validation,omitempty"`
}
```

### Kustomization execution

The kustomization `spec.interval` tells the controller at which interval to fetch the Kubernetes manifest for the source,
build the kustomization and apply it on the cluster.
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

### Kustomization source

The kustomization `spec.sourceRef` is a reference to an object managed by
[source-controller](https://github.com/fluxcd/source-controller). When the source
[revision](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/common.md#source-status) 
changes, it creates a Kubernetes event that triggers a kustomization apply outside of the specified interval.

Source supported types:

* [GitRepository](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/gitrepositories.md)

### Garbage collection

Garbage collection means that the Kubernetes objects that were previously applied on the cluster
but are missing from the current apply, will be removed. Garbage collection is also performed when a Kustomization
object is deleted, triggering a removal of all Kubernetes objects previously applied on the cluster.

When garbage collection is enabled, all Kubernetes objects must have a common label that matches the `spec.prune`
[label selector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/).

For example, `prune: env=dev,app=frontend` requires a `kustomization.yaml` with `commonLabels`:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
commonLabels:
  env: dev
  app: frontend
```

## Status

The status sub-resource describes the result of the last kustomization execution.

```go
type KustomizationStatus struct {
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
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

Wait for condition example:

```bash
kubectl wait kustomization/podinfo --for=condition=ready --timeout=1m
```



