# Profile

The `Profile` API defines a common behavior for a group of [Kustomization](kustomization.md) objects. 

## Specification

```go
type ProfileSpec struct {
	// Alerting configuration of the kustomizations targeted by this profile.
	// +optional
	Alert *AlertProvider `json:"alert"`

	// The list of kustomizations that this profile applies to.
	// +required
	Kustomizations []string `json:"kustomizations"`
}
```

Alerting configuration:

```go
type AlertProvider struct {
	// HTTP(S) webhook address of this provider
	// +required
	Address string `json:"address"`

	// Alert channel for this provider
	// +required
	Channel string `json:"channel"`

	// Bot username for this provider
	// +required
	Username string `json:"username"`

	// Filter alerts based on verbosity level, defaults to ('error').
	// +kubebuilder:validation:Enum=info;error
	// +optional
	Verbosity string `json:"verbosity,omitempty"`

	// Type of provider
	// +kubebuilder:validation:Enum=slack;discord
	// +required
	Type string `json:"type"`
}
```

Status condition types:

```go
const (
	// ReadyCondition represents the fact that a given Profile has been
	// processed by the controller.
	ReadyCondition string = "Ready"
)
```

Status condition reasons:

```go
const (
	// InitializedReason represents the fact that a given resource has been initialized.
	InitializedReason string = "Initialized"
)
```

## Alerting

Alerting can be configured by creating a profile that contains an alert definition:

```yaml
apiVersion: kustomize.fluxcd.io/v1alpha1
kind: Profile
metadata:
  name: default
spec:
  alert:
    type: slack
    verbosity: info
    address: https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK
    username: kustomize-controller
    channel: general
  kustomizations:
    - '*'
```

The alert provider type can be: `slack` or `discord` and the verbosity can be set to `info` or `error`.

The `*` wildcard tells the controller to use this profile for all kustomizations that are present
in the same namespace as the profile.
Multiple profiles can be used to send alerts to different channels or Slack organizations.

When the verbosity is set to `error`, the controller will alert on any error encountered during the
reconciliation process. This includes kustomize build and validation errors, apply errors and
health check failures.

When the verbosity is set to `info`, the controller will alert whenever a kustomization status changes.

