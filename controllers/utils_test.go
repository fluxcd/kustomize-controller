package controllers

import (
	"errors"
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

func TestStripSensitiveData(t *testing.T) {
	tests := []struct {
		name     string
		in       error
		expected error
	}{
		{
			"stringData",
			errors.New("apply failed: Error from server (BadRequest): error when creating \"0f1563ce-8273-4879-99dd-f6f58629cc2d.yaml\": Secret in version \"v1\" cannot be handled as a Secret: v1.Secret.StringData: ReadString: expects \" or n, but found 0, error found in #10 byte of ...|\"secret\":0}}\n|..., bigger context ...|\"namespace\":\"sensitive-data-dkgvw\"},\"stringData\":{\"secret\":0}}\n|...\n"),
			errors.New("apply failed: Error from server (BadRequest): error when creating \"0f1563ce-8273-4879-99dd-f6f58629cc2d.yaml\": Secret in version \"v1\" cannot be handled as a Secret: v1.Secret.StringData: [ ** REDACTED ** ]\n|..., bigger context ...|\"namespace\":\"sensitive-data-dkgvw\"},\"stringData\":{ [ ** REDACTED ** ] }\n|...\n"),
		},
		{
			"data",
			errors.New("apply failed: Error from server (BadRequest): error when creating \"0f1563ce-8273-4879-99dd-f6f58629cc2d.yaml\": Secret in version \"v1\" cannot be handled as a Secret: v1.Secret.Data: ReadString: expects \" or n, but found 0, error found in #10 byte of ...|\"secret\":0}}\n|..., bigger context ...|\"namespace\":\"sensitive-data-dkgvw\"},\"data\":{\"secret\":0}}\n|...\n"),
			errors.New("apply failed: Error from server (BadRequest): error when creating \"0f1563ce-8273-4879-99dd-f6f58629cc2d.yaml\": Secret in version \"v1\" cannot be handled as a Secret: v1.Secret.Data: [ ** REDACTED ** ]\n|..., bigger context ...|\"namespace\":\"sensitive-data-dkgvw\"},\"data\":{ [ ** REDACTED ** ] }\n|...\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected := stripSensitiveData(tt.in)

			if expected.Error() != tt.expected.Error() {
				t.Errorf("\nexpected:\n%q\ngot:\n%q\n", tt.expected.Error(), expected.Error())
			}
		})
	}
}
