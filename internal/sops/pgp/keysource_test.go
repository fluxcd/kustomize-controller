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

package pgp

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"go.mozilla.org/sops/v3/pgp"
)

var (
	mockPublicKey   = "testdata/public.gpg"
	mockPrivateKey  = "testdata/private.gpg"
	mockFingerprint = "B59DAF469E8C948138901A649732075EA221A7EA"
)

func TestMasterKeyFromFingerprint(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint(mockFingerprint, "")
	g.Expect(key.Fingerprint).To(Equal(mockFingerprint))
	g.Expect(key.CreationDate).Should(BeTemporally("~", time.Now(), time.Second))
	g.Expect(key.homeDir).To(BeEmpty())

	key = MasterKeyFromFingerprint("B59DAF 469E8C94813 8901A 649732075E A221A7EA", "")
	g.Expect(key.Fingerprint).To(Equal(mockFingerprint))

	key = MasterKeyFromFingerprint(mockFingerprint, "/some/path")
	g.Expect(key.homeDir).To(Equal("/some/path"))
}

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPublicKey)
	g.Expect(err).NotTo(HaveOccurred())

	key := MasterKeyFromFingerprint(mockFingerprint, gpgHome)
	data := []byte("oh no, my darkest secret")
	g.Expect(key.Encrypt(data)).To(Succeed())

	g.Expect(key.EncryptedKey).ToNot(BeEmpty())
	g.Expect(key.EncryptedKey).ToNot(Equal(data))

	err, _, _ = gpgExec(gpgHome, []string{"--import", mockPrivateKey}, nil)
	g.Expect(err).ToNot(HaveOccurred())

	args := []string{
		"-d",
	}
	err, stdout, stderr := gpgExec(key.gpgHome(), args, strings.NewReader(key.EncryptedKey))
	g.Expect(err).ToNot(HaveOccurred(), stderr.String())
	g.Expect(stdout.Bytes()).To(Equal(data))

	key.Fingerprint = "invalid"
	err = key.Encrypt([]byte("invalid"))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(Equal("gpg: 'invalid' is not a valid long keyID"))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPrivateKey)
	g.Expect(err).NotTo(HaveOccurred())

	dataKey := []byte("foo")

	encryptKey := MasterKeyFromFingerprint(mockFingerprint, gpgHome)
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	t.Setenv("GNUPGHOME", gpgHome)
	decryptKey := pgp.NewMasterKeyFromFingerprint(mockFingerprint)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPrivateKey)
	g.Expect(err).NotTo(HaveOccurred())

	key := MasterKeyFromFingerprint(mockFingerprint, gpgHome)
	g.Expect(key.EncryptIfNeeded([]byte("data"))).To(Succeed())

	encryptedKey := key.EncryptedKey
	g.Expect(encryptedKey).To(ContainSubstring("END PGP MESSAGE"))

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
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPrivateKey)
	g.Expect(err).NotTo(HaveOccurred())

	fingerprint := shortenFingerprint(mockFingerprint)

	data := []byte("this data is absolutely top secret")
	err, stdout, stderr := gpgExec(gpgHome, []string{
		"--no-default-recipient",
		"--yes",
		"--encrypt",
		"-a",
		"-r",
		fingerprint,
		"--trusted-key",
		fingerprint,
		"--no-encrypt-to",
	}, bytes.NewReader(data))
	g.Expect(err).NotTo(HaveOccurred(), stderr.String())

	encryptedData := stdout.String()
	g.Expect(encryptedData).ToNot(BeEquivalentTo(data))

	key := MasterKeyFromFingerprint(mockFingerprint, gpgHome)
	key.EncryptedKey = encryptedData

	got, err := key.Decrypt()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(data))

	key.EncryptedKey = "absolute invalid"
	got, err = key.Decrypt()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("gpg: no valid OpenPGP data found"))
	g.Expect(got).To(BeNil())
}

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPrivateKey)
	g.Expect(err).NotTo(HaveOccurred())

	dataKey := []byte("foo")

	t.Setenv("GNUPGHOME", gpgHome)
	encryptKey := pgp.NewMasterKeyFromFingerprint(mockFingerprint)
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	decryptKey := MasterKeyFromFingerprint(mockFingerprint, gpgHome)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)
	tmpDir := t.TempDir()

	gpgHome, err := mockGpgHome(tmpDir, mockPrivateKey)
	g.Expect(err).ToNot(HaveOccurred())

	key := MasterKeyFromFingerprint(mockFingerprint, gpgHome)

	data := []byte("some secret data")
	g.Expect(key.Encrypt(data)).To(Succeed())
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())

	decryptedData, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(data))
}

func TestMasterKey_NeedsRotation(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint("", "")
	g.Expect(key.NeedsRotation()).To(BeFalse())

	key.CreationDate = key.CreationDate.Add(-(pgpTTL + time.Second))
	g.Expect(key.NeedsRotation()).To(BeTrue())
}

func TestMasterKey_ToString(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint(mockFingerprint, "")
	g.Expect(key.ToString()).To(Equal(mockFingerprint))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint(mockFingerprint, "")
	key.EncryptedKey = "data"
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"fp":         mockFingerprint,
		"created_at": key.CreationDate.UTC().Format(time.RFC3339),
		"enc":        key.EncryptedKey,
	}))
}

func TestMasterKey_gpgHome(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{}

	usr, err := user.Current()
	if err == nil {
		g.Expect(key.gpgHome()).To(Equal(filepath.Join(usr.HomeDir, ".gnupg")))
	} else {
		g.Expect(key.gpgHome()).To(Equal(filepath.Join(os.Getenv("HOME"), ".gnupg")))
	}

	gnupgHome := "/overwrite/home"
	t.Setenv("GNUPGHOME", gnupgHome)
	g.Expect(key.gpgHome()).To(Equal(gnupgHome))

	key.homeDir = "/home/dir/overwrite"
	g.Expect(key.gpgHome()).To(Equal(key.homeDir))
}

func Test_gpgBinary(t *testing.T) {
	g := NewWithT(t)

	g.Expect(gpgBinary()).To(Equal("gpg"))

	overwrite := "/some/other/gpg"
	t.Setenv(SopsGpgExecEnv, overwrite)
	g.Expect(gpgBinary()).To(Equal(overwrite))
}

func Test_shortenFingerprint(t *testing.T) {
	g := NewWithT(t)

	shortId := shortenFingerprint(mockFingerprint)
	g.Expect(shortId).To(Equal("9732075EA221A7EA"))

	g.Expect(shortenFingerprint(shortId)).To(Equal(shortId))
}

func mockGpgHome(dir string, key string) (string, error) {
	gpgHome := filepath.Join(dir, "gpghome")
	// This is required as otherwise GPG complains about the permissions
	// of the directory.
	if err := os.Mkdir(gpgHome, 0700); err != nil {
		return "", err
	}
	if key != "" {
		err, _, stderr := gpgExec(gpgHome, []string{"--import", key}, nil)
		if err != nil {
			return "", fmt.Errorf(stderr.String())
		}
	}
	return gpgHome, nil
}
