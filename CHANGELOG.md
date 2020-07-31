# Changelog

All notable changes to this project are documented in this file.

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
using the [notification.fluxcd.io API](https://github.com/fluxcd/notification-controller/tree/master/docs/spec).

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
[Profile CRD](https://github.com/fluxcd/kustomize-controller/blob/master/docs/spec/v1alpha1/profile.md)
that allows grouping
[Kustomization](https://github.com/fluxcd/kustomize-controller/blob/master/docs/spec/v1alpha1/kustomization.md)
objects and defining a common behavior for them.
The v1alpha1 profiles can be used for
[configuring Slack and Discord alerting](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.2#configure-alerting).

## 0.0.1-alpha.1 (2020-04-20)

This is the first alpha release of kustomize controller.
The controller is an implementation of the
[kustomize.fluxcd.io/v1alpha1](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.1/docs/spec/v1alpha1) API.
