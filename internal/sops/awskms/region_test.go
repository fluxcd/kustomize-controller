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
	for _, tt := range []struct {
		arn      string
		expected string
	}{
		{
			arn:      "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48",
			expected: "us-west-2",
		},
		{
			arn:      "arn:aws-cn:kms:cn-north-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab",
			expected: "cn-north-1",
		},
		{
			arn:      "arn:aws-us-gov:kms:us-gov-west-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab",
			expected: "us-gov-west-1",
		},
		{
			arn:      "arn:aws:kms:us-west-2:107501996527:alias/my-key-alias",
			expected: "us-west-2",
		},
		{
			arn:      "arn:aws:kms:us-west-2:107501996527:key/",
			expected: "",
		},
		{
			arn:      "arn:aws:kms:us-west-2:107501996527:alias/",
			expected: "",
		},
		{
			arn:      "not-an-arn",
			expected: "",
		},
		{
			arn:      "arn:aws:s3:::my-bucket",
			expected: "",
		},
		{
			arn:      "arn:aws:ec2:us-west-2:123456789012:instance/i-1234567890abcdef0",
			expected: "",
		},
		{
			arn:      "arn:aws:iam::123456789012:user/David",
			expected: "",
		},
		{
			arn:      "arn:aws:lambda:us-west-2:123456789012:function:my-function",
			expected: "",
		},
		{
			arn:      "arn:aws:dynamodb:us-west-2:123456789012:table/my-table",
			expected: "",
		},
		{
			arn:      "arn:aws:rds:us-west-2:123456789012:db:my-database",
			expected: "",
		},
		{
			arn:      "arn:aws:sns:us-west-2:123456789012:my-topic",
			expected: "",
		},
	} {
		t.Run(tt.arn, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(awskms.GetRegionFromKMSARN(tt.arn)).To(Equal(tt.expected))
		})
	}
}
