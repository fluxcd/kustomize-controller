# kustomize-controller

[![e2e](https://github.com/fluxcd/kustomize-controller/workflows/e2e/badge.svg)](https://github.com/fluxcd/kustomize-controller/actions)

The kustomize-controller is a Kubernetes operator that applies kustomizations in-cluster.

![overview](docs/diagrams/fluxcd-kustomize-source-controllers.png)

Features:
* watches for `Kustomization` objects
* fetches artifacts produced by [source-controller](https://github.com/fluxcd/source-controller) from `Source` objects 
* watches `Source` objects for revision changes 
* builds the kustomization using the latest fetched artifact
* validates the Kubernetes objects with client-side or APIServer dry-run
* applies the resulting Kubernetes manifests on the cluster
* prunes the Kubernetes objects removed from source based on a label selector

## Kustomization API

A kustomization object defines the source of Kubernetes manifests by referencing a source
(managed by [source-controller](https://github.com/fluxcd/source-controller)),
the path to the kustomization file, 
and a label selector used for garbage collection of resources removed from the Git source.

### Specification

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

	// Validate the Kubernetes objects before applying them on the cluster.
	// The validation strategy can be 'client' (local dry-run) or 'server' (APIServer dry-run).
	// +kubebuilder:validation:Enum=client;server
	// +optional
	Validation string `json:"validation,omitempty"`
}
```

### Supported source kinds

* [GitRepository](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/gitrepositories.md)

### Garbage collection

Garbage collection means that the Kubernetes objects that were previously applied on the cluster
but are missing from the current apply, will be removed. Garbage collection is also performed when a Kustomization
object is deleted, triggering a removal of all Kubernetes objects previously applied on the cluster.

When garbage collection is enabled, all Kubernetes objects must have a common label that matches the `prune`
[label selector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/).

For example, `prune: env=dev` requires a `kustomization.yaml` with `commonLabels`:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
commonLabels:
  env: dev
```

## Usage

Build prerequisites:
* go >= 1.13
* kubebuilder
* kustomize
* kubectl

### Install the controllers

Install source-controller with:

```bash
kustomize build https://github.com/fluxcd/source-controller//config/default?ref=v0.0.1-alpha.1 \
kubectl apply -f-
```

Install kustomize-controller with:

```bash
git clone https://github.com/fluxcd/kustomize-controller
cd kustomize-controller
make docker-build docker-push dev-deploy IMG=your-docker-hub-username/kustomize-controller:test
```

### Define a Git repository source

Create a source object that points to a Git repository containing Kubernetes and Kustomize manifests:

```yaml
apiVersion: source.fluxcd.io/v1alpha1
kind: GitRepository
metadata:
  name: podinfo
  namespace: default
spec:
  interval: 1m
  url: https://github.com/stefanprodan/podinfo-deploy
  ref:
    branch: master
```

For private repositories, SSH or token based authentication can be
[configured with Kubernetes secrets](https://github.com/fluxcd/source-controller/blob/master/docs/spec/v1alpha1/gitrepositories.md).

Save the above file and apply it on the cluster.
You can wait for the source controller to assemble an artifact from the head of the repo master branch with:

```bash
kubectl wait gitrepository/podinfo --for=condition=ready
```

The source controller will check for new commits in the master branch every minute. You can force a git sync with:

```bash
kubectl annotate --overwrite gitrepository/podinfo source.fluxcd.io/syncAt="$(date +%s)"
```

### Define a kustomization

Create a kustomization object that uses the git repository defined above:

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: podinfo-dev
spec:
  interval: 5m
  path: "./overlays/dev/"
  prune: "env=dev"
  sourceRef:
    kind: GitRepository
    name: podinfo
  validation: client
```

With `spec.path` we tell the controller where to look for the `kustomization.yaml` file.
With `spec.prune` we configure garbage collection.
With `spec.interval` we tell the controller how often it should reconcile the cluster state.
With `spec.validation` we instruct the controller to validate the Kubernetes objects before
applying them in-cluster. When setting the validation to `server`, the controller will perform an
[APIServer dry-run](https://kubernetes.io/blog/2019/01/14/apiserver-dry-run-and-kubectl-diff/)
(requires Kubernetes >= 1.16).

Save the above file and apply it on the cluster.
You can wait for the kustomize controller to apply the manifest corresponding to the dev overlay with:

```bash
kubectl wait kustomization/podinfo-dev --for=condition=ready
```

When the controller finishes the reconciliation, it will log the applied objects:

```bash
kubectl -n kustomize-system logs deploy/kustomize-controller | jq .
```

```json
{
  "level": "info",
  "ts": 1587195448.071468,
  "logger": "controllers.Kustomization",
  "msg": "Kustomization applied in 1.436096591s",
  "kustomization": "default/podinfo-dev",
  "output": {
    "namespace/dev": "created",
    "service/podinfo": "created",
    "deployment.apps/podinfo": "created",
    "horizontalpodautoscaler.autoscaling/podinfo": "created"
  }
}
```

You can trigger a kustomize build and apply any time with:

```bash
kubectl annotate --overwrite kustomization/podinfo-dev kustomize.fluxcd.io/syncAt="$(date +%s)"
```

When the source controller pulls a new Git revision, the kustomize controller will detect that the
source revision changed, and will apply those changes right away.

If the kustomization build or apply fails, the controller sets the ready condition to `false` and logs the error:

```yaml
status:
  conditions:
  - lastTransitionTime: "2020-04-16T07:27:58Z"
    message: 'apply failed'
    reason: ApplyFailed
    status: "False"
    type: Ready
``` 

```json
{
  "kustomization": "default/podinfo-dev",
  "error": "Error from server (NotFound): error when creating \"podinfo-dev.yaml\": namespaces \"dev\" not found\n"
}
```

### Deploy releases to production

For production deployments, instead of synchronizing with a branch you can use a semver range to target stable releases:

```yaml
apiVersion: source.fluxcd.io/v1alpha1
kind: GitRepository
metadata:
  name: podinfo-releases
spec:
  interval: 5m
  url: https://github.com/stefanprodan/podinfo-deploy
  ref:
    semver: ">=0.0.1-rc.1 <1.0.0"
```

With `ref.semver` we configure source controller to pull the Git tags and create an artifact from the most recent tag
that matches the semver range.

Create a production kustomization and reference the git source that follows the latest semver release:

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Kustomization
metadata:
  name: podinfo-production
spec:
  interval: 10m
  path: "./overlays/production/"
  prune: "env=production"
  sourceRef:
    kind: GitRepository
    name: podinfo-releases
```

Based on the above definition, the kustomize controller will build and apply a kustomization that matches the semver range
set in the Git repository manifest.

## GitOps workflow

Example:
* create a `GitRepository` per app (example repo [podinfo-deploy](https://github.com/stefanprodan/podinfo-deploy))
* create a `Kustomization` per app/environment 
* push changes to a kustomize overlay in git
* source controller pulls the changes from git and creates an artifact
* kustomize controller fetches the latest artifact
* kustomize controller builds the overlay manifest and applies it on the cluster
* push a git tag in semver format
* source controller pulls the git tag and creates an artifact
* kustomize controller fetches the artifact and applies it to production
