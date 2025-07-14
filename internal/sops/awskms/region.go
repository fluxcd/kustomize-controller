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

package awskms

import (
	"regexp"
)

// arnRegex matches an AWS ARN, for example:
// "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48".
// The regex matches both KMS keys and aliases, and supports different AWS partition names (aws, aws-cn, aws-us-gov).
//
// Copied from SOPS:
// https://github.com/getsops/sops/blob/b2edaade23453c8774fc28ec491ddbe2b9a4c994/kms/keysource.go#L30-L32
//
// ref:
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html
var arnRegex = regexp.MustCompile(`^arn:aws[\w-]*:kms:(.+):[0-9]+:(key|alias)/.+$`)

// GetRegionFromKMSARN extracts the region from a KMS ARN.
func GetRegionFromKMSARN(arn string) string {
	m := arnRegex.FindStringSubmatch(arn)
	if m == nil {
		return ""
	}
	return m[1]
}
