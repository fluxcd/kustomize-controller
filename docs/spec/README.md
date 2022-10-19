# Kustomize Controller

The kustomize-controller is a Kubernetes operator, specialized in running 
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

When a cluster is shared with multiple teams, a cluster admin may wish to assign roles and service
accounts to each team. The manifests owned by a team will be applied on the cluster using
the team's account thus ensuring isolation between teams. For example, an admin can 
restrict the operations performed on the cluster by a team to a single namespace.

When dealing with an incident, one may wish to suspend the reconciliation of some workloads and
pin the reconciliation of others to a specific Git revision, without having to stop the reconciler
and affect the whole cluster.

When operating a cluster, different teams may wish to receive notification about the status
of their CD pipelines. For example, the on-call team would receive alerts about all
failures in the prod namespace, while the frontend team may wish to be alerted when a new version 
of the frontend app was deployed and if the deployment is healthy, no matter the namespace.

## Design

The reconciliation process can be defined with a Kubernetes custom resource
that describes a pipeline such as:
- **check** if depends-on conditions are meet  
- **fetch** manifests from source-controller
- **generate** `kustomization.yaml` if needed
- **build** the manifests using the Kustomize SDK
- **decrypt** Kubernetes secrets using Mozilla SOPS SDK
- **impersonate** the tenant's Kubernetes account
- **validate** the resulting objects using server-side apply dry-run
- **detect drift** between the desired and state and cluster state
- **correct drift** by applying the objects using server-side apply
- **prune** the objects removed from source
- **wait** for the applied changes to rollout using Kubernetes kstatus library
- **report** the reconciliation result in the `status` sub-resource
- **alert** if something went wrong by sending events to Kubernetes API and notification-controller
- **notify** if the cluster state changed by sending events to Kubernetes API and notification-controller

## Specifications

The latest API specifications can be found [here](v1beta2/README.md).
