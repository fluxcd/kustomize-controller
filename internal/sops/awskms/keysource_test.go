/*
Copyright (C) 2022 The Flux authors

This Source Code Form is subject to the terms of the Mozilla Public
License, v. 2.0. If a copy of the MPL was not distributed with this
file, You can obtain one at https://mozilla.org/MPL/2.0/.
*/

package awskms

import (
	"context"
	"encoding/base64"
	"fmt"
	logger "log"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	sessionv1 "github.com/aws/aws-sdk-go/aws/session"
	kmsv1 "github.com/aws/aws-sdk-go/service/kms"
	. "github.com/onsi/gomega"
	"github.com/ory/dockertest/v3"
)

var (
	testKMSServerURL string
	testKMSARN       string
)

const (
	dummyARN          = "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48"
	testLocalKMSTag   = "3.11.1"
	testLocalKMSImage = "nsmithuk/local-kms"
)

// TestMain initializes a AWS KMS server using Docker, writes the HTTP address to
// testAWSEndpoint, tries to generate a key for encryption-decryption using a
// backoff retry approach and then sets testKMSARN to the id of the generated key.
// It then runs all the tests, which can make use of the various `test*` variables.
func TestMain(m *testing.M) {
	// Uses a sensible default on Windows (TCP/HTTP) and Linux/MacOS (socket)
	pool, err := dockertest.NewPool("")
	if err != nil {
		logger.Fatalf("could not connect to docker: %s", err)
	}

	// Pull the image, create a container based on it, and run it
	// resource, err := pool.Run("nsmithuk/local-kms", testLocalKMSVersion, []string{})
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository:   testLocalKMSImage,
		Tag:          testLocalKMSTag,
		ExposedPorts: []string{"8080"},
	})
	if err != nil {
		logger.Fatalf("could not start resource: %s", err)
	}

	purgeResource := func() {
		if err := pool.Purge(resource); err != nil {
			logger.Printf("could not purge resource: %s", err)
		}
	}

	testKMSServerURL = fmt.Sprintf("http://127.0.0.1:%v", resource.GetPort("8080/tcp"))
	masterKey := createTestMasterKey(dummyARN)

	kmsClient, err := createTestKMSClient(masterKey)
	if err != nil {
		purgeResource()
		logger.Fatalf("could not create session: %s", err)
	}

	var key *kms.CreateKeyOutput
	if err := pool.Retry(func() error {
		key, err = kmsClient.CreateKey(context.TODO(), &kms.CreateKeyInput{})
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		purgeResource()
		logger.Fatalf("could not create key: %s", err)
	}

	if key.KeyMetadata.Arn != nil {
		testKMSARN = *key.KeyMetadata.Arn
	} else {
		purgeResource()
		logger.Fatalf("could not set arn")
	}

	// Run the tests, but only if we succeeded in setting up the AWS KMS server.
	var code int
	if err == nil {
		code = m.Run()
	}

	// This can't be deferred, as os.Exit simpy does not care
	if err := pool.Purge(resource); err != nil {
		logger.Fatalf("could not purge resource: %s", err)
	}

	os.Exit(code)
}

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)
	key := createTestMasterKey(testKMSARN)
	dataKey := []byte("thisistheway")
	g.Expect(key.Encrypt(dataKey)).To(Succeed())
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())

	kmsClient, err := createTestKMSClient(key)
	g.Expect(err).ToNot(HaveOccurred())

	k, err := base64.StdEncoding.DecodeString(key.EncryptedKey)
	g.Expect(err).ToNot(HaveOccurred())

	input := &kms.DecryptInput{
		CiphertextBlob:    k,
		EncryptionContext: key.EncryptionContext,
	}
	decrypted, err := kmsClient.Decrypt(context.TODO(), input)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decrypted.Plaintext).To(Equal(dataKey))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	encryptKey := createTestMasterKey(testKMSARN)
	dataKey := []byte("encrypt-compat")
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	// This is the core decryption logic of `sopskms.MasterKey.Decrypt()`.
	// We don't call `sops.MasterKey.Decrypt()` directly to avoid issues with
	// session and config setup.
	config := awsv1.Config{
		Region:   awsv1.String("us-west-2"),
		Endpoint: &testKMSServerURL,
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	k, err := base64.StdEncoding.DecodeString(encryptKey.EncryptedKey)
	g.Expect(err).ToNot(HaveOccurred())
	sess, err := sessionv1.NewSessionWithOptions(sessionv1.Options{
		Config: config,
	})
	kmsSvc := kmsv1.New(sess)
	decrypted, err := kmsSvc.Decrypt(&kmsv1.DecryptInput{CiphertextBlob: k})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decrypted.Plaintext).To(Equal(dataKey))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)

	key := createTestMasterKey(testKMSARN)
	g.Expect(key.EncryptIfNeeded([]byte("data"))).To(Succeed())

	encryptedKey := key.EncryptedKey
	g.Expect(encryptedKey).ToNot(BeEmpty())

	g.Expect(key.EncryptIfNeeded([]byte("some other data"))).To(Succeed())
	g.Expect(key.EncryptedKey).To(Equal(encryptedKey))
}

func TestMasterKey_EncryptedDataKey(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{EncryptedKey: "some key"}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))
}

func TestMasterKey_Decrypt(t *testing.T) {
	g := NewWithT(t)

	key := createTestMasterKey(testKMSARN)
	kmsClient, err := createTestKMSClient(key)
	g.Expect(err).ToNot(HaveOccurred())

	dataKey := []byte("itsalwaysdns")
	out, err := kmsClient.Encrypt(context.TODO(), &kms.EncryptInput{
		Plaintext: dataKey, KeyId: &key.Arn, EncryptionContext: key.EncryptionContext,
	})
	g.Expect(err).ToNot(HaveOccurred())

	key.EncryptedKey = base64.StdEncoding.EncodeToString(out.CiphertextBlob)
	got, err := key.Decrypt()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(dataKey))
}

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	// This is the core encryption logic of `sopskms.MasterKey.Encrypt()`.
	// We don't call `sops.MasterKey.Encrypt()` directly to avoid issues with
	// session and config setup.
	dataKey := []byte("decrypt-compat")
	config := awsv1.Config{
		Region:   awsv1.String("us-west-2"),
		Endpoint: &testKMSServerURL,
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	sess, err := sessionv1.NewSessionWithOptions(sessionv1.Options{
		Config: config,
	})
	kmsSvc := kmsv1.New(sess)
	encrypted, err := kmsSvc.Encrypt(&kmsv1.EncryptInput{Plaintext: dataKey, KeyId: &testKMSARN})
	g.Expect(err).ToNot(HaveOccurred())

	decryptKey := createTestMasterKey(testKMSARN)
	decryptKey.EncryptedKey = base64.StdEncoding.EncodeToString(encrypted.CiphertextBlob)
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	dataKey := []byte("thisistheway")

	encryptKey := createTestMasterKey(testKMSARN)
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())
	g.Expect(encryptKey.EncryptedKey).ToNot(BeEmpty())

	decryptKey := createTestMasterKey(testKMSARN)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey

	decryptedData, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(dataKey))
}

func TestMasterKey_NeedsRotation(t *testing.T) {
	g := NewWithT(t)

	key := NewMasterKeyFromArn(dummyARN, nil, "")
	g.Expect(key.NeedsRotation()).To(BeFalse())

	key.CreationDate = key.CreationDate.Add(-(kmsTTL + time.Second))
	g.Expect(key.NeedsRotation()).To(BeTrue())
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)
	key := MasterKey{
		Arn:          "test-arn",
		Role:         "test-role",
		EncryptedKey: "enc-key",
		EncryptionContext: map[string]string{
			"env": "test",
		},
	}
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"arn":        "test-arn",
		"role":       "test-role",
		"created_at": "0001-01-01T00:00:00Z",
		"enc":        "enc-key",
		"context": map[string]string{
			"env": "test",
		},
	}))
}

func TestCreds_ApplyToMasterKey(t *testing.T) {
	g := NewWithT(t)

	creds := CredsProvider{
		credsProvider: credentials.NewStaticCredentialsProvider("", "", ""),
	}
	key := &MasterKey{}
	creds.ApplyToMasterKey(key)
	g.Expect(key.credentialsProvider).To(Equal(creds.credsProvider))
}

func TestLoadAwsKmsCredsFromYaml(t *testing.T) {
	g := NewWithT(t)
	credsYaml := []byte(`
aws_access_key_id: test-id
aws_secret_access_key: test-secret
aws_session_token: test-token
`)
	credsProvider, err := LoadCredsProviderFromYaml(credsYaml)
	g.Expect(err).ToNot(HaveOccurred())

	creds, err := credsProvider.credsProvider.Retrieve(context.TODO())
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(creds.AccessKeyID).To(Equal("test-id"))
	g.Expect(creds.SecretAccessKey).To(Equal("test-secret"))
	g.Expect(creds.SessionToken).To(Equal("test-token"))
}

func Test_createKMSConfig(t *testing.T) {
	tests := []struct {
		name       string
		key        MasterKey
		assertFunc func(g *WithT, cfg *aws.Config, err error)
		fallback   bool
	}{
		{
			name: "master key with invalid arn fails",
			key: MasterKey{
				Arn: "arn:gcp:kms:antartica-north-2::key/45e6-aca6-a5b005693a48",
			},
			assertFunc: func(g *WithT, _ *aws.Config, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("no valid ARN found"))
			},
		},
		{
			name: "master key with with proper configuration passes",
			key: MasterKey{
				credentialsProvider: credentials.NewStaticCredentialsProvider("test-id", "test-secret", "test-token"),
				AwsProfile:          "test-profile",
				Arn:                 "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48",
			},
			assertFunc: func(g *WithT, cfg *aws.Config, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cfg.Region).To(Equal("us-west-2"))

				creds, err := cfg.Credentials.Retrieve(context.TODO())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(creds.AccessKeyID).To(Equal("test-id"))
				g.Expect(creds.SecretAccessKey).To(Equal("test-secret"))
				g.Expect(creds.SessionToken).To(Equal("test-token"))

				// ConfigSources is a slice of config.Config, which in turn is an interface.
				// Since we use a LoadOptions object, we assert the type of cfgSrc and then
				// check if the expected profile is present.
				for _, cfgSrc := range cfg.ConfigSources {
					if src, ok := cfgSrc.(config.LoadOptions); ok {
						g.Expect(src.SharedConfigProfile).To(Equal("test-profile"))
					}
				}
			},
		},
		{
			name: "master key without creds and profile falls back to the environment variables",
			key: MasterKey{
				Arn: "arn:aws:kms:us-west-2:107501996527:key/612d5f0p-p1l3-45e6-aca6-a5b005693a48",
			},
			fallback: true,
			assertFunc: func(g *WithT, cfg *aws.Config, err error) {
				g.Expect(err).ToNot(HaveOccurred())

				creds, err := cfg.Credentials.Retrieve(context.TODO())
				g.Expect(creds.AccessKeyID).To(Equal("id"))
				g.Expect(creds.SecretAccessKey).To(Equal("secret"))
				g.Expect(creds.SessionToken).To(Equal("token"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// Set the environment variables if we want to fallback
			if tt.fallback {
				t.Setenv("AWS_ACCESS_KEY_ID", "id")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
				t.Setenv("AWS_SESSION_TOKEN", "token")
			}
			cfg, err := tt.key.createKMSConfig()
			tt.assertFunc(g, cfg, err)
		})
	}
}

func createTestMasterKey(arn string) MasterKey {
	return MasterKey{
		Arn:                 arn,
		credentialsProvider: credentials.NewStaticCredentialsProvider("id", "secret", ""),
		epResolver:          epResolver{},
	}
}

// epResolver is a dummy resolver that points to the local test KMS server
type epResolver struct{}

func (e epResolver) ResolveEndpoint(service, region string, options ...interface{}) (aws.Endpoint, error) {
	return aws.Endpoint{
		URL: testKMSServerURL,
	}, nil
}

func createTestKMSClient(key MasterKey) (*kms.Client, error) {
	cfg, err := key.createKMSConfig()
	if err != nil {
		return nil, err
	}

	cfg.EndpointResolverWithOptions = epResolver{}

	return kms.NewFromConfig(*cfg), nil
}
