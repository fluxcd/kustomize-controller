package controllers

import (
	"strings"
	"testing"
)

func Test_parseApplyError(t *testing.T) {
	filtered := parseApplyError([]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged
ingressroute.traefik.containo.us/flux-receiver configured
service/notification-controller created
The Service "webhook-receiver" is invalid: spec.clusterIP: Invalid value: "10.200.133.61": field is immutable`))
	filtered = strings.TrimSpace(filtered)
	numLines := len(strings.Split(filtered, "\n"))
	if numLines != 1 {
		t.Errorf("Should filter out all but one line from the error output, but got %d lines", numLines)
	}
}

func Test_parseApplyError_dryRun(t *testing.T) {
	filtered := parseApplyError([]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged (dry run)
ingressroute.traefik.containo.us/flux-receiver configured (dry run)
service/notification-controller created (dry run)
error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container`))
	filtered = strings.TrimSpace(filtered)
	numLines := len(strings.Split(filtered, "\n"))
	if numLines != 1 {
		t.Errorf("Should filter out all but one line from the error output, but got %d lines", numLines)
	}
}
