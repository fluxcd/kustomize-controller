/*
Copyright 2025 The Flux authors

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

package awskms_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/fluxcd/kustomize-controller/internal/sops/awskms"
)

func TestGetRegionFromKMSARN(t *testing.T) {
	g := NewWithT(t)

	arn := "arn:aws:kms:us-east-1:211125720409:key/mrk-3179bb7e88bc42ffb1a27d5038ceea25"

	region := awskms.GetRegionFromKMSARN(arn)
	g.Expect(region).To(Equal("us-east-1"))
}
