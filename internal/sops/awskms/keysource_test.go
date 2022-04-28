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

package awskms

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws/credentials"
	. "github.com/onsi/gomega"
)

func TestCreds_ApplyToMasterKey(t *testing.T) {
	g := NewWithT(t)

	creds := Creds{
		credentials: credentials.NewStaticCredentials("test-id", "test-secret", "test-token"),
	}
	key := &MasterKey{}
	creds.ApplyToMasterKey(key)
	g.Expect(key.credentials).To(Equal(creds.credentials))
}

func TestLoadAwsKmsCredsFromYaml(t *testing.T) {
	g := NewWithT(t)
	credsYaml := []byte(`
aws_access_key_id: test-id
aws_secret_access_key: test-secret
aws_session_token: test-token
    `)
	creds, err := LoadAwsKmsCredsFromYaml(credsYaml)
	g.Expect(err).ToNot(HaveOccurred())
	value, err := creds.credentials.Get()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(value.AccessKeyID).To(Equal("test-id"))
	g.Expect(value.SecretAccessKey).To(Equal("test-secret"))
	g.Expect(value.SessionToken).To(Equal("test-token"))
}

func Test_createSession(t *testing.T) {
	g := NewWithT(t)
	creds := credentials.NewStaticCredentials("test-id", "test-secret", "")
	key := MasterKey{
		Arn:         "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48",
		credentials: creds,
	}
	sess, err := key.createSession()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(sess).ToNot(BeNil())
}
