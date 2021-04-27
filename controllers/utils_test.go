package controllers

import (
	"strings"
	"testing"
)

func TestParseApplyError(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		filtered string
	}{
		{
			"apply",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged
ingressroute.traefik.containo.us/flux-receiver configured
service/notification-controller created
The Service "webhook-receiver" is invalid: spec.clusterIP: Invalid value: "10.200.133.61": field is immutable
`),
			`The Service "webhook-receiver" is invalid: spec.clusterIP: Invalid value: "10.200.133.61": field is immutable`,
		},
		{
			"client dry-run",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged (dry run)
ingressroute.traefik.containo.us/flux-receiver configured (dry run)
service/notification-controller created (dry run)
error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container
`),
			`error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container`,
		},
		{
			"server dry-run",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged (server dry run)
ingressroute.traefik.containo.us/flux-receiver configured (server dry run)
service/notification-controller created (server dry run)
error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container
`),
			`error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := parseApplyError(tt.in)
			filtered = strings.TrimSpace(filtered)

			if tt.filtered != filtered {
				t.Errorf("expected %q, but actual %q", tt.filtered, filtered)
			}
		})
	}
}
