package controller

import sourcev1 "github.com/fluxcd/source-controller/api/v1"

type ArtifactSource interface {
	GetArtifact() *sourcev1.Artifact
}

var _ ArtifactSource = sourcev1.Source(nil)
