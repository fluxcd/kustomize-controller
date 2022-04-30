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
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"encoding/base64"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"sigs.k8s.io/yaml"
)

const (
	arnRegex        = `^arn:aws[\w-]*:kms:(.+):[0-9]+:(key|alias)/.+$`
	stsSessionRegex = "[^a-zA-Z0-9=,.@-]+"
	// kmsTTL is the duration after which a MasterKey requires rotation.
	kmsTTL = time.Hour * 24 * 30 * 6
)

// MasterKey is a AWS KMS key used to encrypt and decrypt sops' data key.
type MasterKey struct {
	Arn               string
	Role              string
	EncryptedKey      string
	CreationDate      time.Time
	EncryptionContext map[string]string

	credentialsProvider aws.CredentialsProvider

	// epResolver IS ONLY MEANT TO BE USED FOR TESTS.
	// it can be used to override the endpoint that the AWS client resolves to
	// by default. it's hacky but there is no other choice, since you can't
	// specify the endpoint as an env var like you can do with an access key.
	epResolver aws.EndpointResolver
}

// CredsProvider is a wrapper around aws.CredentialsProvider used for authenticating
// when using AWS KMS.
type CredsProvider struct {
	credsProvider aws.CredentialsProvider
}

// NewCredsProvider returns a Creds object with the provided aws.CredentialsProvider
func NewCredsProvider(cp aws.CredentialsProvider) *CredsProvider {
	return &CredsProvider{
		credsProvider: cp,
	}
}

// ApplyToMasterKey configures the credentials the provided key.
func (c CredsProvider) ApplyToMasterKey(key *MasterKey) {
	key.credentialsProvider = c.credsProvider
}

// LoadAwsKmsCredsProviderFromYaml parses the given yaml returns a CredsProvider object
// which contains the credentials provider used for authenticating towards AWS KMS.
func LoadAwsKmsCredsProviderFromYaml(b []byte) (*CredsProvider, error) {
	credInfo := struct {
		AccessKeyID     string `json:"aws_access_key_id"`
		SecretAccessKey string `json:"aws_secret_access_key"`
		SessionToken    string `json:"aws_session_token"`
	}{}
	if err := yaml.Unmarshal(b, &credInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AWS credentials file: %w", err)
	}
	return &CredsProvider{
		credsProvider: credentials.NewStaticCredentialsProvider(credInfo.AccessKeyID,
			credInfo.SecretAccessKey, credInfo.SessionToken),
	}, nil
}

// EncryptedDataKey returns the encrypted data key this master key holds
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Encrypt takes a sops data key, encrypts it with KMS and stores the result in the EncryptedKey field
func (key *MasterKey) Encrypt(dataKey []byte) error {
	cfg, err := key.createKMSConfig()
	if err != nil {
		return err
	}
	client := kms.NewFromConfig(*cfg)
	input := &kms.EncryptInput{
		KeyId:     &key.Arn,
		Plaintext: dataKey,
	}
	out, err := client.Encrypt(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key with AWS KMS: %w", err)
	}
	key.EncryptedKey = base64.StdEncoding.EncodeToString(out.CiphertextBlob)
	return nil
}

// EncryptIfNeeded encrypts the provided sops' data key and encrypts it if it hasn't been encrypted yet
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with AWS KMS and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(key.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("error base64-decoding encrypted data key: %s", err)
	}
	cfg, err := key.createKMSConfig()
	if err != nil {
		return nil, err
	}
	client := kms.NewFromConfig(*cfg)
	input := &kms.DecryptInput{
		KeyId:             &key.Arn,
		CiphertextBlob:    k,
		EncryptionContext: key.EncryptionContext,
	}
	decrypted, err := client.Decrypt(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt sops data key with AWS KMS: %w", err)
	}
	return decrypted.Plaintext, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate) > (time.Hour * 24 * 30 * 6)
}

// ToString converts the key to a string representation
func (key *MasterKey) ToString() string {
	return key.Arn
}

// NewMasterKey creates a new MasterKey from an ARN, role and context, setting the creation date to the current date
func NewMasterKey(arn string, role string, context map[string]string) *MasterKey {
	return &MasterKey{
		Arn:               arn,
		Role:              role,
		EncryptionContext: context,
		CreationDate:      time.Now().UTC(),
	}
}

// NewMasterKeyFromArn takes an ARN string and returns a new MasterKey for that ARN
func NewMasterKeyFromArn(arn string, context map[string]string, awsProfile string) *MasterKey {
	k := &MasterKey{}
	arn = strings.Replace(arn, " ", "", -1)
	roleIndex := strings.Index(arn, "+arn:aws:iam::")
	if roleIndex > 0 {
		k.Arn = arn[:roleIndex]
		k.Role = arn[roleIndex+1:]
	} else {
		k.Arn = arn
	}
	k.EncryptionContext = context
	k.CreationDate = time.Now().UTC()
	return k
}

func (key MasterKey) createKMSConfig() (*aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), func(lo *config.LoadOptions) error {
		if key.credentialsProvider != nil {
			lo.Credentials = key.credentialsProvider
		}
		if key.epResolver != nil {
			lo.EndpointResolver = key.epResolver
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't load AWS config: %w", err)
	}
	if key.Role != "" {
		return key.createSTSConfig(&cfg)
	}

	return &cfg, nil
}

func (key MasterKey) createSTSConfig(config *aws.Config) (*aws.Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	stsRoleSessionNameRe, err := regexp.Compile(stsSessionRegex)
	if err != nil {
		return nil, fmt.Errorf("failed to compile STS role session name regex: %w", err)
	}
	sanitizedHostname := stsRoleSessionNameRe.ReplaceAllString(hostname, "")
	name := "sops@" + sanitizedHostname

	client := sts.NewFromConfig(*config)
	input := &sts.AssumeRoleInput{
		RoleArn:         &key.Arn,
		RoleSessionName: &name,
	}
	out, err := client.AssumeRole(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to assume role '%s': %w", key.Role, err)
	}
	config.Credentials = credentials.NewStaticCredentialsProvider(*out.Credentials.AccessKeyId,
		*out.Credentials.SecretAccessKey, *out.Credentials.SessionToken,
	)
	return config, nil
}

// ToMap converts the MasterKey to a map for serialization purposes
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["arn"] = key.Arn
	if key.Role != "" {
		out["role"] = key.Role
	}
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	if key.EncryptionContext != nil {
		outcontext := make(map[string]string)
		for k, v := range key.EncryptionContext {
			outcontext[k] = v
		}
		out["context"] = outcontext
	}
	return out
}
