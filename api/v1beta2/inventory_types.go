/*
Copyright 2021 The Flux authors

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

package v1beta2

// Inventory is a record of objects that have been reconciled.
type Inventory struct {
	// Entries of Kubernetes objects IDs.
	Entries []Entry `json:"entries"`
}

// Entry contains the information necessary to locate a resource within a cluster.
type Entry struct {
	// ObjectID is the string representation of a Kubernetes object metadata,
	// in the format '<namespace>_<name>_<group>_<kind>'.
	ObjectID string `json:"id"`

	// ObjectVersion is the API version of this  Kubernetes object kind.
	ObjectVersion string `json:"v"`
}
