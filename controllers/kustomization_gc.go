package controllers

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-logr/logr"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

type KustomizeGarbageCollector struct {
	snapshot kustomizev1.Snapshot
	log      logr.Logger
}

func NewGarbageCollector(snapshot kustomizev1.Snapshot, log logr.Logger) *KustomizeGarbageCollector {
	return &KustomizeGarbageCollector{
		snapshot: snapshot,
		log:      log,
	}
}

func (kgc *KustomizeGarbageCollector) Prune(timeout time.Duration, name string, namespace string) (string, bool) {
	selector := kgc.selectors(name, namespace, kgc.snapshot.Revision)
	ok := true
	changeSet := ""
	outInfo := ""
	outErr := ""
	for ns, kinds := range kgc.snapshot.NamespacedKinds() {
		for _, kind := range kinds {
			if output, err := kgc.deleteByKind(timeout, kind, ns, selector); err != nil {
				outErr += " " + err.Error()
				ok = false
			} else {
				outInfo += " " + output + "\n"
			}
		}
	}
	if outErr == "" {
		kgc.log.Info("Garbage collection for namespaced objects completed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name),
			"output", outInfo)
		changeSet += outInfo
	} else {

		kgc.log.Error(fmt.Errorf(outErr), "Garbage collection for namespaced objects failed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name))
	}

	outInfo = ""
	outErr = ""
	for _, kind := range kgc.snapshot.NonNamespacedKinds() {
		if output, err := kgc.deleteByKind(timeout, kind, "", selector); err != nil {
			outErr += " " + err.Error()
			ok = false
		} else {
			outInfo += " " + output + "\n"
		}
	}
	if outErr == "" {
		kgc.log.Info("Garbage collection for non-namespaced objects completed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name),
			"output", outInfo)
		changeSet += outInfo
	} else {
		kgc.log.Error(fmt.Errorf(outErr), "Garbage collection for non-namespaced objects failed",
			"kustomization", fmt.Sprintf("%s/%s", namespace, name))
	}

	return changeSet, ok
}

func (kgc *KustomizeGarbageCollector) deleteByKind(timeout time.Duration, kind, namespace, selector string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()

	cmd := fmt.Sprintf("kubectl delete %s -l %s", kind, selector)
	if namespace != "" {
		cmd = fmt.Sprintf("%s -n=%s", cmd, namespace)
	}

	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	if output, err := command.CombinedOutput(); err != nil {
		// ignore unknown resource kind
		if strings.Contains(string(output), "the server doesn't have a resource type") {
			return strings.TrimSuffix(string(output), "\n"), nil
		} else {
			return "", fmt.Errorf("%s", strings.TrimSuffix(string(output), "\n"))
		}
	} else {
		return strings.TrimSuffix(string(output), "\n"), nil
	}
}

func (kgc *KustomizeGarbageCollector) selectors(name, namespace, revision string) string {
	return fmt.Sprintf("kustomization/name=%s-%s,kustomization/revision=%s", name, namespace, checksum(revision))
}

func gcLabels(name, namespace, revision string) map[string]string {
	return map[string]string{
		"kustomization/name":     fmt.Sprintf("%s-%s", name, namespace),
		"kustomization/revision": checksum(revision),
	}
}

func checksum(in string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(in)))
}
