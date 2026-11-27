package controller

import (
	"context"
	"testing"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/runtime/events"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func TestWarningHandler(t *testing.T) {
	tests := []struct {
		name        string
		code        int
		message     string
		withContext bool
		wantEvent   bool
	}{
		{
			name:        "records API server warning",
			code:        299,
			message:     `unknown field "spec.foo"`,
			withContext: true,
			wantEvent:   true,
		},
		{
			name:    "ignores warning without Kustomization context",
			code:    299,
			message: `unknown field "spec.foo"`,
		},
		{
			name:        "ignores empty warning",
			code:        299,
			withContext: true,
		},
		{
			name:        "ignores non-warning response",
			code:        200,
			message:     "some message",
			withContext: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			ctx := context.Background()
			obj := &kustomizev1.Kustomization{
				Name:      "test",
				Namespace: "default",
			}

			if tt.withContext {
				ctx = contextWithKustomization(ctx, obj)
			}

			logger := &fakeWarningLogger{}
			recorder := events.NewFakeRecorder(2, true)

			h := &warningHandler{
				logger:   logger,
				recorder: recorder,
			}

			h.HandleWarningHeaderWithContext(
				ctx, tt.code, "test-agent", tt.message,
			)

			// All warnings must be forwarded to the logger.
			g.Expect(logger.calls).To(HaveLen(1))
			g.Expect(logger.calls[0]).To(Equal(warningCall{
				ctx:     ctx,
				code:    tt.code,
				agent:   "test-agent",
				message: tt.message,
			}))

			events := recorder.GetEvents()
			// Only eligible warnings should produce events.
			if !tt.wantEvent {
				g.Expect(events).To(BeEmpty())
				return
			}

			g.Expect(events).To(HaveLen(1))

			event := events[0]
			g.Expect(event.InvolvedObject).To(Equal(corev1.ObjectReference{
				APIVersion: obj.APIVersion,
				Kind:       obj.Kind,
				Namespace:  obj.Namespace,
				Name:       obj.Name,
			}))
			g.Expect(event.Related).To(BeNil())
			g.Expect(event.Type).To(Equal(corev1.EventTypeWarning))
			g.Expect(event.Reason).To(Equal(apiServerWarningReason))
			g.Expect(event.Action).To(Equal(kustomizev1.ActionReconcile.String()))
			g.Expect(event.Message).To(Equal(tt.message))
		})
	}
}

type fakeWarningLogger struct {
	calls []warningCall
}

type warningCall struct {
	ctx     context.Context
	code    int
	agent   string
	message string
}

func (f *fakeWarningLogger) HandleWarningHeaderWithContext(
	ctx context.Context,
	code int,
	agent, message string,
) {
	f.calls = append(f.calls, warningCall{ctx, code, agent, message})
}
