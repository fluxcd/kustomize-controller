/*
Copyright 2022 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/fluxcd/pkg/untar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/hashicorp/go-retryablehttp"
)

// ArtifactFetcher holds the HTTP client that reties with back off when
// the artifact server is offline.
type ArtifactFetcher struct {
	httpClient *retryablehttp.Client
}

// ArtifactNotFoundError is an error type used to signal 404 HTTP status code responses.
var ArtifactNotFoundError = errors.New("artifact not found")

// NewArtifactFetcher configures the retryable http client used for fetching artifacts.
// By default, it retries 10 times within a 3.5 minutes window.
func NewArtifactFetcher(retries int) *ArtifactFetcher {
	httpClient := retryablehttp.NewClient()
	httpClient.RetryWaitMin = 5 * time.Second
	httpClient.RetryWaitMax = 30 * time.Second
	httpClient.RetryMax = retries
	httpClient.Logger = nil

	return &ArtifactFetcher{httpClient: httpClient}
}

// Fetch downloads, verifies and extracts the artifact content to the specified directory.
// If the artifact server responds with 5xx errors, the download operation is retried.
// If the artifact server responds with 404, the returned error is of type ArtifactNotFoundError.
// If the artifact server is unavailable for more than 3 minutes, the returned error contains the original status code.
func (r *ArtifactFetcher) Fetch(artifact *sourcev1.Artifact, dir string) error {
	artifactURL := artifact.URL
	if hostname := os.Getenv("SOURCE_CONTROLLER_LOCALHOST"); hostname != "" {
		u, err := url.Parse(artifactURL)
		if err != nil {
			return err
		}
		u.Host = hostname
		artifactURL = u.String()
	}

	req, err := retryablehttp.NewRequest(http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create a new request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download artifact, error: %w", err)
	}
	defer resp.Body.Close()

	if code := resp.StatusCode; code != http.StatusOK {
		if code == http.StatusNotFound {
			return ArtifactNotFoundError
		}
		return fmt.Errorf("failed to download artifact from %s, status: %s", artifactURL, resp.Status)
	}

	var buf bytes.Buffer

	// verify checksum matches origin
	if err := r.Verify(artifact, &buf, resp.Body); err != nil {
		return err
	}

	// extract
	if _, err = untar.Untar(&buf, dir); err != nil {
		return fmt.Errorf("failed to untar artifact, error: %w", err)
	}

	return nil
}

// Verify computes the checksum of the tarball and returns an error if the computed value
// does not match the artifact advertised checksum.
func (r *ArtifactFetcher) Verify(artifact *sourcev1.Artifact, buf *bytes.Buffer, reader io.Reader) error {
	hasher := sha256.New()

	// for backwards compatibility with source-controller v0.17.2 and older
	if len(artifact.Checksum) == 40 {
		hasher = sha1.New()
	}

	// compute checksum
	mw := io.MultiWriter(hasher, buf)
	if _, err := io.Copy(mw, reader); err != nil {
		return err
	}

	if checksum := fmt.Sprintf("%x", hasher.Sum(nil)); checksum != artifact.Checksum {
		return fmt.Errorf("failed to verify artifact: computed checksum '%s' doesn't match advertised '%s'",
			checksum, artifact.Checksum)
	}

	return nil
}
