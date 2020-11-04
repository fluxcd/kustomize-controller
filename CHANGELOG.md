# Changelog

All notable changes to this project are documented in this file.

## 0.2.1 (2020-11-04)

This prerelease comes with improvements to garbage collection.
The Kubernetes packages have been updated to v1.19.

Improvements:
* Update k8s to 1.19 + kustomize 0.6.4
    [#161](https://github.com/fluxcd/kustomize-controller/pull/161)
* Add openssh client to support git+ssh Kustomize resources
    [#159](https://github.com/fluxcd/kustomize-controller/pull/159)

Fixes:
* Use latest generation when updating final status
    [#164](https://github.com/fluxcd/kustomize-controller/pull/164)
* Omit checksum label if GC is disabled
    [#162](https://github.com/fluxcd/kustomize-controller/pull/162)

## 0.2.0 (2020-10-29)

This is the second MINOR prerelease, it comes with breaking changes:
* the histogram metric `gotk_reconcile_duration` was renamed to `gotk_reconcile_duration_seconds`
* the annotation `fluxcd.io/reconcileAt` was renamed to `reconcile.fluxcd.io/requestedAt`

Improvements:
* Refactor predicates and enqueuers
    [#156](https://github.com/fluxcd/kustomize-controller/pull/156)
    [#155](https://github.com/fluxcd/kustomize-controller/pull/155)
    [#153](https://github.com/fluxcd/kustomize-controller/pull/153)
* Use annotation helpers
    [#152](https://github.com/fluxcd/kustomize-controller/pull/152)
* Suppress health check events when no changes made
    [#151](https://github.com/fluxcd/kustomize-controller/pull/151)
* Use controller-runtime utils for finalizer and health checks
    [#150](https://github.com/fluxcd/kustomize-controller/pull/150)
* Improve remote cluster documentation
    [#148](https://github.com/fluxcd/kustomize-controller/pull/148)

## 0.1.2 (2020-10-16)

This prerelease comes with support for
[targeting remote clusters](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1beta1/kustomization.md#cluster-api--remote-clusters)
created with Cluster-API.

Improvements:
* Implement non-caching, per-kustomization GC-client/statusPoller for cross-cluster kubeconfigs
    [#135](https://github.com/fluxcd/kustomize-controller/pull/135)

Fixes:
* Fix status reporting when the source is not found
    [#141](https://github.com/fluxcd/kustomize-controller/pull/141)
* Validate manifests when generating kustomization.yaml
    [#143](https://github.com/fluxcd/kustomize-controller/pull/143)
* Set correct status on failure events
    [#145](https://github.com/fluxcd/kustomize-controller/pull/145)

## 0.1.1 (2020-10-13)

This prerelease comes with Prometheus instrumentation for the controller's resources.

For each kind, the controller exposes a gauge metric to track the `Ready` condition status,
and a histogram with the reconciliation duration in seconds:

* `gotk_reconcile_condition{kind, name, namespace, status, type="Ready"}`
* `gotk_reconcile_duration{kind, name, namespace}`

## 0.1.0 (2020-09-30)

This is the first MINOR prerelease, it promotes the
`kustomize.toolkit.fluxcd.io` API to `v1beta1`
and removes support for `v1alpha1`.

Going forward, changes to the API will be accompanied by a conversion
mechanism. With this release the API becomes more stable, but while in
beta phase there are no guarantees about backwards compatibility
between beta releases.

## 0.0.13 (2020-09-22)

This prerelease comes with support for S3 bucket sources and
cross-namespace dependencies.
Container images for ARMv7 and ARMv8 are published to
`ghcr.io/fluxcd/kustomize-controller-arm64`.

## 0.0.12 (2020-09-14)

This prerelease comes with improvements to health assessment.
The health checks leverage the kstatus library to support
custom resources such as HelmReleases that implement the Ready condition.

## 0.0.11 (2020-09-12)

This prerelease comes with the option to watch for resources
in the runtime namespace of the controller or at cluster level.

## 0.0.10 (2020-09-05)

This prerelease comes with a bug fix to garbage collection.

## 0.0.9 (2020-09-04)

This prerelease comes with support for decrypting Kubernetes secrets
with Mozilla SOPS.
Container images for linux/amd64 and linux/arm64 are published to GHCR.

## 0.0.8 (2020-08-18)

This prerelease upgrades the `github.com/fluxcd/pkg/*` dependencies to
dedicated versioned modules, and makes the `api` package available as
a dedicated versioned module.

## 0.0.7 (2020-07-31)

This prerelease comes with a breaking change, the CRDs group has been
renamed to `kustomize.toolkit.fluxcd.io`. The dependency on `source-controller`
has been updated to `v0.0.7` to be able to work with `source.toolkit.fluxcd.io`
resources.

## 0.0.6 (2020-07-25)

This prerelease comes with improvements to the dependency management
and service account impersonation.

## 0.0.5 (2020-07-20)

This prerelease drops support for Kubernetes <1.16.
The CRDs have been updated to `apiextensions.k8s.io/v1`.

## 0.0.4 (2020-07-16)

This prerelease comes with improvements to the alerting system
and allows sources to be referenced across namespaces.
The kustomize/api has been updated to v0.5.1.

## 0.0.3 (2020-07-13)

This prerelease comes with improvements to logging.
The default logging format is JSON and the timestamp format is ISO8601.
Introduce `fluxcd.io/reconcileAt` annotation for on-demand reconciliation
of kustomization objects.

## 0.0.2 (2020-07-02)

This prerelease comes with improvements to the alerting system.
The reconciliation events can be forwarded to notification controller
and alerting can be configured for Slack, MS Teams, Discord and Rocket chat
using the [notification.fluxcd.io API](https://github.com/fluxcd/notification-controller/tree/main/docs/spec).

## 0.0.1 (2020-06-24)

This is the first prerelease ready for public testing. To get started
testing, see the [GitOps Toolkit guide](https://toolkit.fluxcd.io/get-started/).

## 0.0.1-beta.2 (2020-06-10)

This beta release allows configuring the number of concurrent reconciles.
Starting with this version, the controller watches for resources
only in the namespace where it's deployed.

## 0.0.1-beta.1 (2020-05-29)

This is the first beta release of kustomize controller.
This release comes with improvements to the reconciliation engine when 
dealing with CRDs/CRs. The kustomize/api has been updated to v0.4.1.

## 0.0.1-alpha.9 (2020-05-11)

This alpha release includes a bug fix for the source event handler
and sets the current context to the default namespace.

## 0.0.1-alpha.8 (2020-05-09)

This alpha release comes with improvements to health assessment
and [dependency management](https://github.com/fluxcd/kustomize-controller/pull/32).
When a source revision changes, the
Kustomizations are executed based on the depends-on graph.

## 0.0.1-alpha.7 (2020-05-05)

This alpha release comes with improvements to the garbage collector.
The new GC doesn't require label selectors to be set in the kustomization
and can prune resources safely without hitting Kubernetes API rate limits.

## 0.0.1-alpha.6 (2020-05-03)

This alpha release comes with
[role-based access control](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.6/docs/spec/v1alpha1/kustomization.md#role-based-access-control)
for restricting the execution of a kustomization apply to a specific service account.

## 0.0.1-alpha.5 (2020-04-27)

This alpha release introduces an [intermediate state](https://github.com/fluxcd/kustomize-controller/pull/21)
to the status ready condition to signal that a reconciliation is underway.
This allows waiting for an on-demand sync to complete.

## 0.0.1-alpha.4 (2020-04-24)

This alpha release introduces a new status field for recording the
[last applied source revision](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.4/docs/spec/v1alpha1/kustomization.md#status).

Feature comparison with Flux has been added to
[docs/spec](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.4/docs/spec/README.md#backward-compatibility).

## 0.0.1-alpha.3 (2020-04-23)

This alpha release introduces the option to tell the controller to
[automatically generate](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.3/docs/spec/v1alpha1/kustomization.md#generate-kustomizationyaml)
the `kustomization.yaml` for repositories that contain plain Kubernetes manifests.

The controller design and motivation can be found at
[docs/spec](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.3/docs/spec).

## 0.0.1-alpha.2 (2020-04-21)

This alpha release introduces the
[Profile CRD](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1alpha1/profile.md)
that allows grouping
[Kustomization](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1alpha1/kustomization.md)
objects and defining a common behavior for them.
The v1alpha1 profiles can be used for
[configuring Slack and Discord alerting](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.2#configure-alerting).

## 0.0.1-alpha.1 (2020-04-20)

This is the first alpha release of kustomize controller.
The controller is an implementation of the
[kustomize.fluxcd.io/v1alpha1](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.1/docs/spec/v1alpha1) API.
