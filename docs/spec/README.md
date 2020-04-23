# Kustomize Controller

The Kustomize Controller is a Kubernetes operator, specialized in running 
continuous delivery pipelines for infrastructure and workloads
defined with Kubernetes manifests and assembled with Kustomize.

## Motivation

The main goal is to provide an automated operator that can
bootstrap and continuously reconcile the cluster state
from multiple sources (e.g. infrastructure and application repositories).

When provisioning a new cluster, one may wish to install workloads in a specific order,
for example a validation controller such as OPA Gatekeeper should be up and running before 
applying other manifests on the cluster. Another example is a service mesh admission controller,
the proxy injector must be functional before deploying applications into the mesh.

When operating a cluster, different teams may wish to receive notification about the status
of their CD pipelines. For example, the on-call team would receive alerts about all
failures in the prod namespace, while the frontend team may wish to be alerted when a new version 
of the frontend app was deployed and if the deployment is healthy, no matter the namespace.

When dealing with an incident, one may wish to suspend the reconciliation of some workloads and
pin the reconciliation of others to a specific Git revision, without having to stop the reconciler
and affect the whole cluster.

## Design

The reconciliation process can be defined with a Kubernetes custom resource
that describes a pipeline such as:
- **check** if depends-on conditions are meet  
- **fetch** manifests from Git repository X
- **generate** a kustomization if needed
- **build** the manifest using kustomization X
- **validate** the resulting objects 
- **apply** the objects 
- **prune** the objects removed from source
- **verify** the deployment status
- **alert** if something went wrong
- **notify** if the cluster state changed 

The controller the runs these pipelines relies on
[source-controller](https://github.com/fluxcd/source-controller)
for providing the raw manifests from Git repositories or any
other sources that source-controller could support in the future. 

If a Git repository contains no Kustomize manifests, the controller will
generate the `kustomization.yaml` automatically and label
the objects for garbage collection (GC).

A pipeline runs on-a-schedule and ca be triggered manually by a
cluster admin or automatically by a source event such as a Git revision change.

When a pipeline is removed from the cluster, the controller's GC terminates
all the objects previously created by that pipeline.

A pipeline can be suspended, while in suspension the controller will
stop the scheduler and will ignore any source events.
Deleting a suspended pipeline does not trigger garbage collection.

Alerting can be configured with a Kubernetes custom resource
that specifies a webhook address, and a group of pipelines to be monitored.

The API design of the controller can be found at [kustomize.fluxcd.io/v1alpha1](v1alpha1/README.md).
