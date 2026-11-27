package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	controllerLog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/fluxcd/pkg/runtime/events"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

const apiServerWarningReason = "APIServerWarning"

type warningHandler struct {
	logger   rest.WarningHandlerWithContext
	recorder events.Recorder
}

func newWarningHandler(recorder events.Recorder) *warningHandler {
	return &warningHandler{
		logger: controllerLog.NewKubeAPIWarningLogger(
			controllerLog.KubeAPIWarningLoggerOptions{
				Deduplicate: false,
			},
		),
		recorder: recorder,
	}
}

func (h *warningHandler) HandleWarningHeaderWithContext(
	ctx context.Context,
	code int,
	agent string,
	text string,
) {
	// Preserve controller-runtime's default warning logging.
	h.logger.HandleWarningHeaderWithContext(ctx, code, agent, text)

	if code != 299 || text == "" {
		return
	}

	kustomization, ok := kustomizationFromContext(ctx)
	if !ok {
		return
	}

	h.recorder.Eventf(kustomization, nil, corev1.EventTypeWarning, apiServerWarningReason, kustomizev1.ActionReconcile.String(), text)
}

type kustomizationContextKey struct{}

func contextWithKustomization(
	ctx context.Context,
	obj *kustomizev1.Kustomization,
) context.Context {
	return context.WithValue(ctx, kustomizationContextKey{}, obj)
}

func kustomizationFromContext(
	ctx context.Context,
) (*kustomizev1.Kustomization, bool) {
	obj, ok := ctx.Value(kustomizationContextKey{}).(*kustomizev1.Kustomization)
	return obj, ok
}
