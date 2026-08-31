/*
Copyright 2026 The Flux authors

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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
)

// TempDirPrefix is the name prefix of the temporary directories
// the reconciler extracts source artifacts into.
const TempDirPrefix = "kustomization-"

// ListStaleTempDirs returns the absolute paths of the directories in root
// whose name starts with TempDirPrefix. It must be called before the
// reconcilers start, so that every match is a leftover of a previous
// process that exited without running its deferred cleanup.
func ListStaleTempDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", root, err)
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), TempDirPrefix) {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}
	return dirs, nil
}

// PurgeTempDirs removes the given directories, logging every removal at
// info level and every failure at error level. It stops as soon as ctx is
// done and returns the number of directories that were removed.
func PurgeTempDirs(ctx context.Context, log logr.Logger, dirs []string) int {
	purged := 0
	for i, dir := range dirs {
		if err := ctx.Err(); err != nil {
			log.Error(err, "aborted purge of stale tmp dirs",
				"purged", purged, "remaining", len(dirs)-i)
			return purged
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Error(err, "failed to remove stale tmp dir", "path", dir)
			continue
		}
		log.Info("removed stale tmp dir", "path", dir)
		purged++
	}
	return purged
}
