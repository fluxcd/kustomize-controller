/*
Copyright (C) 2022 The Flux authors

This Source Code Form is subject to the terms of the Mozilla Public
License, v. 2.0. If a copy of the MPL was not distributed with this
file, You can obtain one at https://mozilla.org/MPL/2.0/.
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
	// arnRegex matches an AWS ARN.
	// valid ARN example: arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48
	arnRegex = `^arn:aws[\w-]*:kms:(.+):[0-9]+:(key|alias)/.+$`
	// stsSessionRegex matches an AWS STS session name.
	// valid STS session examples: john_s, sops@42WQm042
	stsSessionRegex = "[^a-zA-Z0-9=,.@-_]+"
	// kmsTTL is the duration after which a MasterKey requires rotation.
	kmsTTL = time.Hour * 24 * 30 * 6
	// roleSessionNameLengthLimit is the AWS role session name length limit.
	roleSessionNameLengthLimit = 64
)

// MasterKey is an AWS KMS key used to encrypt and decrypt sops' data key.
// Adapted from: https://github.com/mozilla/sops/blob/v3.7.2/kms/keysource.go#L39
// Modified to accept custom static credentials as opposed to using env vars by default
// and use aws-sdk-go-v2 instead of aws-sdk-go being used in upstream.
type MasterKey struct {
	// AWS Role ARN associated with the KMS key.
	Arn string
	// AWS Role ARN used to assume a role through AWS STS.
	Role string
	// EncryptedKey stores the data key in it's encrypted form.
	EncryptedKey string
	// CreationDate is when this MasterKey was created.
	CreationDate time.Time
	// EncryptionContext provides additional context about the data key.
	// Ref: https://docs.aws.amazon.com/kms/latest/developerguide/concepts.html#encrypt_context
	EncryptionContext map[string]string
	// AWSProfile is the profile to use for loading configuration and credentials.
	AwsProfile string

	// credentialsProvider is used to configure the AWS config with the
	// necessary credentials.
	credentialsProvider aws.CredentialsProvider

	// epResolver can be used to override the endpoint the AWS client resolves
	// to by default. This is mostly used for testing purposes as it can not be
	// injected using e.g. an environment variable. The field is not publicly
	// exposed, nor configurable.
	epResolver aws.EndpointResolverWithOptions
}

// CredsProvider is a wrapper around aws.CredentialsProvider used for authenticating
// towards AWS KMS.
type CredsProvider struct {
	credsProvider aws.CredentialsProvider
}

// NewCredsProvider returns a CredsProvider object with the provided aws.CredentialsProvider.
func NewCredsProvider(cp aws.CredentialsProvider) *CredsProvider {
	return &CredsProvider{
		credsProvider: cp,
	}
}

// ApplyToMasterKey configures the credentials the provided key.
func (c CredsProvider) ApplyToMasterKey(key *MasterKey) {
	key.credentialsProvider = c.credsProvider
}

// LoadCredsProviderFromYaml parses the given YAML returns a CredsProvider object
// which contains the credentials provider used for authenticating towards AWS KMS.
func LoadCredsProviderFromYaml(b []byte) (*CredsProvider, error) {
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

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Encrypt takes a SOPS data key, encrypts it with KMS and stores the result
// in the EncryptedKey field.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	cfg, err := key.createKMSConfig()
	if err != nil {
		return err
	}
	client := kms.NewFromConfig(*cfg)
	input := &kms.EncryptInput{
		KeyId:             &key.Arn,
		Plaintext:         dataKey,
		EncryptionContext: key.EncryptionContext,
	}
	out, err := client.Encrypt(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key with AWS KMS: %w", err)
	}
	key.EncryptedKey = base64.StdEncoding.EncodeToString(out.CiphertextBlob)
	return nil
}

// EncryptIfNeeded encrypts the provided sops' data key and encrypts it, if it
// has not been encrypted yet.
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
	return time.Since(key.CreationDate) > kmsTTL
}

// ToString converts the key to a string representation.
func (key *MasterKey) ToString() string {
	return key.Arn
}

// ToMap converts the MasterKey to a map for serialization purposes.
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

// NewMasterKey creates a new MasterKey from an ARN, role and context, setting the
// creation date to the current date.
func NewMasterKey(arn string, role string, context map[string]string) *MasterKey {
	return &MasterKey{
		Arn:               arn,
		Role:              role,
		EncryptionContext: context,
		CreationDate:      time.Now().UTC(),
	}
}

// NewMasterKeyFromArn takes an ARN string and returns a new MasterKey for that
// ARN.
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
	k.AwsProfile = awsProfile
	return k
}

// createKMSConfig returns a Config configured with the appropriate credentials.
func (key MasterKey) createKMSConfig() (*aws.Config, error) {
	re := regexp.MustCompile(arnRegex)
	matches := re.FindStringSubmatch(key.Arn)
	if matches == nil {
		return nil, fmt.Errorf("no valid ARN found in '%s'", key.Arn)
	}
	region := matches[1]
	cfg, err := config.LoadDefaultConfig(context.TODO(), func(lo *config.LoadOptions) error {
		// Use the credentialsProvider if present, otherwise default to reading credentials
		// from the environment.
		if key.credentialsProvider != nil {
			lo.Credentials = key.credentialsProvider
		}
		if key.AwsProfile != "" {
			lo.SharedConfigProfile = key.AwsProfile
		}
		lo.Region = region

		// Set the epResolver, if present. Used ONLY for tests.
		if key.epResolver != nil {
			lo.EndpointResolverWithOptions = key.epResolver
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

// createSTSConfig uses AWS STS to assume a role and returns a Config configured
// with that role's credentials.
func (key MasterKey) createSTSConfig(config *aws.Config) (*aws.Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	stsRoleSessionNameRe := regexp.MustCompile(stsSessionRegex)

	sanitizedHostname := stsRoleSessionNameRe.ReplaceAllString(hostname, "")
	name := "sops@" + sanitizedHostname
	if len(name) >= roleSessionNameLengthLimit {
		name = name[:roleSessionNameLengthLimit]
	}

	client := sts.NewFromConfig(*config)
	input := &sts.AssumeRoleInput{
		RoleArn:         &key.Role,
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
