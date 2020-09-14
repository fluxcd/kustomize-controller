package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling/aggregator"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling/collector"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling/event"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/cli-utils/pkg/object"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

type KustomizeHealthCheck struct {
	kustomization kustomizev1.Kustomization
	statusPoller  *polling.StatusPoller
}

func NewHealthCheck(kustomization kustomizev1.Kustomization, statusPoller *polling.StatusPoller) *KustomizeHealthCheck {
	return &KustomizeHealthCheck{
		kustomization: kustomization,
		statusPoller:  statusPoller,
	}
}

func (hc *KustomizeHealthCheck) Assess(pollInterval time.Duration) error {
	objMetadata, err := hc.toObjMetadata(hc.kustomization.Spec.HealthChecks)
	if err != nil {
		return err
	}

	timeout := hc.kustomization.GetTimeout() + (time.Second * 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opts := polling.Options{PollInterval: pollInterval, UseCache: true}
	eventsChan := hc.statusPoller.Poll(ctx, objMetadata, opts)
	coll := collector.NewResourceStatusCollector(objMetadata)
	done := coll.ListenWithObserver(eventsChan, collector.ObserverFunc(
		func(statusCollector *collector.ResourceStatusCollector) {
			var rss []*event.ResourceStatus
			for _, rs := range statusCollector.ResourceStatuses {
				rss = append(rss, rs)
			}
			desired := status.CurrentStatus
			aggStatus := aggregator.AggregateStatus(rss, desired)
			if aggStatus == desired {
				cancel()
				return
			}
		}),
	)

	<-done

	if coll.Error != nil {
		return coll.Error
	}

	if ctx.Err() == context.DeadlineExceeded {
		ids := []string{}
		for _, rs := range coll.ResourceStatuses {
			if rs.Status != status.CurrentStatus {
				id := hc.objMetadataToString(rs.Identifier)
				ids = append(ids, id)
			}
		}
		return fmt.Errorf("Health check timed out for [%v]", strings.Join(ids, ", "))
	}

	return nil
}

func (hc *KustomizeHealthCheck) toObjMetadata(cr []kustomizev1.CrossNamespaceObjectReference) ([]object.ObjMetadata, error) {
	oo := []object.ObjMetadata{}
	for _, c := range cr {
		// For backwards compatibility
		if c.APIVersion == "" {
			c.APIVersion = "apps/v1"
		}

		gv, err := schema.ParseGroupVersion(c.APIVersion)
		if err != nil {
			return []object.ObjMetadata{}, err
		}

		gk := schema.GroupKind{Group: gv.Group, Kind: c.Kind}
		o, err := object.CreateObjMetadata(c.Namespace, c.Name, gk)
		if err != nil {
			return []object.ObjMetadata{}, err
		}

		oo = append(oo, o)
	}
	return oo, nil
}

func (hc *KustomizeHealthCheck) objMetadataToString(om object.ObjMetadata) string {
	return fmt.Sprintf("%s '%s/%s'", om.GroupKind.Kind, om.Namespace, om.Name)
}
