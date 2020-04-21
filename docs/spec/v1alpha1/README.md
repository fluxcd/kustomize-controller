# kustomize.fluxcd.io/v1alpha1

This is the v1alpha1 API specification for defining continuous delivery pipelines
of Kubernetes objects generated with Kustomize.

## Specification

- [Kustomization CRD](kustomization.md)
    + [Source reference](kustomization.md#source-reference)
    + [Reconciliation](kustomization.md#reconciliation)
    + [Garbage collection](kustomization.md#garbage-collection)
    + [Health assessment](kustomization.md#health-assessment)
    + [Kustomization dependencies](kustomization.md#kustomization-dependencies)
- [Profile CRD](profile.md)
    + [Alerting configuration](profile.md#alerting)

## Implementation

* [kustomize-controller](https://github.com/fluxcd/kustomize-controller/)
