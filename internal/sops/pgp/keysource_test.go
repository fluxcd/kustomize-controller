// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pgp

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
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

	key := MasterKeyFromFingerprint(mockFingerprint)
	g.Expect(key.Fingerprint).To(Equal(mockFingerprint))
	g.Expect(key.CreationDate).Should(BeTemporally("~", time.Now(), time.Second))

	key = MasterKeyFromFingerprint("B59DAF 469E8C94813 8901A 649732075E A221A7EA")
	g.Expect(key.Fingerprint).To(Equal(mockFingerprint))
}

func TestNewGnuPGHome(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(gnuPGHome.String()).To(BeADirectory())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.Validate()).ToNot(HaveOccurred())
}

func TestGnuPGHome_Import(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})

	b, err := os.ReadFile(mockPublicKey)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gnuPGHome.Import(b)).To(Succeed())

	err, _, stderr := gpgExec(gnuPGHome.String(), []string{"--list-keys", mockFingerprint}, nil)
	g.Expect(err).ToNot(HaveOccurred(), stderr.String())

	b, err = os.ReadFile(mockPrivateKey)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gnuPGHome.Import(b)).To(Succeed())

	err, _, stderr = gpgExec(gnuPGHome.String(), []string{"--list-secret-keys", mockFingerprint}, nil)
	g.Expect(err).ToNot(HaveOccurred(), stderr.String())

	g.Expect(gnuPGHome.Import([]byte("invalid armored data"))).To(HaveOccurred())

	g.Expect(GnuPGHome("").Import(b)).To(HaveOccurred())
}

func TestGnuPGHome_ImportFile(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})

	g.Expect(gnuPGHome.ImportFile(mockPublicKey)).To(Succeed())
	g.Expect(gnuPGHome.ImportFile("invalid")).To(HaveOccurred())
}

func TestGnuPGHome_Validate(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(GnuPGHome("").Validate()).To(HaveOccurred())
	})

	t.Run("relative path", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(GnuPGHome("../../.gnupghome").Validate()).To(HaveOccurred())
	})

	t.Run("file path", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		f, err := os.CreateTemp(tmpDir, "file")
		g.Expect(err).ToNot(HaveOccurred())
		defer f.Close()

		g.Expect(GnuPGHome(f.Name()).Validate()).To(HaveOccurred())
	})

	t.Run("wrong permissions", func(t *testing.T) {
		g := NewWithT(t)

		// Is created with 0755
		tmpDir := t.TempDir()
		g.Expect(GnuPGHome(tmpDir).Validate()).To(HaveOccurred())
	})

	t.Run("valid", func(t *testing.T) {
		g := NewWithT(t)

		gnupgHome, err := NewGnuPGHome()
		g.Expect(err).ToNot(HaveOccurred())
		t.Cleanup(func() {
			_ = os.RemoveAll(gnupgHome.String())
		})
		g.Expect(gnupgHome.Validate()).To(Succeed())
	})
}

func TestGnuPGHome_String(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome := GnuPGHome("/some/absolute/path")
	g.Expect(gnuPGHome.String()).To(Equal("/some/absolute/path"))
}

func TestGnuPGHome_ApplyToMasterKey(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})

	key := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(key)
	g.Expect(key.gnuPGHomeDir).To(Equal(gnuPGHome.String()))

	gnuPGHome = "/non/existing/absolute/path/fails/validate"
	gnuPGHome.ApplyToMasterKey(key)
	g.Expect(key.gnuPGHomeDir).ToNot(Equal(gnuPGHome.String()))
}

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPublicKey)).To(Succeed())

	key := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(key)
	data := []byte("oh no, my darkest secret")
	g.Expect(key.Encrypt(data)).To(Succeed())

	g.Expect(key.EncryptedKey).ToNot(BeEmpty())
	g.Expect(key.EncryptedKey).ToNot(Equal(data))

	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	args := []string{
		"-d",
	}
	err, stdout, stderr := gpgExec(key.gnuPGHome(), args, strings.NewReader(key.EncryptedKey))
	g.Expect(err).ToNot(HaveOccurred(), stderr.String())
	g.Expect(stdout.Bytes()).To(Equal(data))

	key.Fingerprint = "invalid"
	err = key.Encrypt([]byte("invalid"))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(Equal("failed to encrypt sops data key with pgp: gpg: 'invalid' is not a valid long keyID"))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	dataKey := []byte("foo")

	encryptKey := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(encryptKey)
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	t.Setenv("GNUPGHOME", gnuPGHome.String())
	decryptKey := pgp.NewMasterKeyFromFingerprint(mockFingerprint)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	key := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(key)
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

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	fingerprint := shortenFingerprint(mockFingerprint)

	data := []byte("this data is absolutely top secret")
	err, stdout, stderr := gpgExec(gnuPGHome.String(), []string{
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

	key := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(key)
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

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	dataKey := []byte("foo")

	t.Setenv("GNUPGHOME", gnuPGHome.String())
	encryptKey := pgp.NewMasterKeyFromFingerprint(mockFingerprint)
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	decryptKey := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(decryptKey)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey

	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	gnuPGHome, err := NewGnuPGHome()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = os.RemoveAll(gnuPGHome.String())
	})
	g.Expect(gnuPGHome.ImportFile(mockPrivateKey)).To(Succeed())

	key := MasterKeyFromFingerprint(mockFingerprint)
	gnuPGHome.ApplyToMasterKey(key)

	data := []byte("some secret data")
	g.Expect(key.Encrypt(data)).To(Succeed())
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())

	decryptedData, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(data))
}

func TestMasterKey_NeedsRotation(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint("")
	g.Expect(key.NeedsRotation()).To(BeFalse())

	key.CreationDate = key.CreationDate.Add(-(pgpTTL + time.Second))
	g.Expect(key.NeedsRotation()).To(BeTrue())
}

func TestMasterKey_ToString(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint(mockFingerprint)
	g.Expect(key.ToString()).To(Equal(mockFingerprint))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromFingerprint(mockFingerprint)
	key.EncryptedKey = "data"
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"fp":         mockFingerprint,
		"created_at": key.CreationDate.UTC().Format(time.RFC3339),
		"enc":        key.EncryptedKey,
	}))
}

func TestMasterKey_gnuPGHome(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{}

	usr, err := user.Current()
	if err == nil {
		g.Expect(key.gnuPGHome()).To(Equal(filepath.Join(usr.HomeDir, ".gnupg")))
	} else {
		g.Expect(key.gnuPGHome()).To(Equal(filepath.Join(os.Getenv("HOME"), ".gnupg")))
	}

	gnupgHome := "/overwrite/home"
	t.Setenv("GNUPGHOME", gnupgHome)
	g.Expect(key.gnuPGHome()).To(Equal(gnupgHome))

	key.gnuPGHomeDir = "/home/dir/overwrite"
	g.Expect(key.gnuPGHome()).To(Equal(key.gnuPGHomeDir))
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

func Fuzz_Pgp(f *testing.F) {
	f.Fuzz(func(t *testing.T, seed, data []byte) {
		fc := fuzz.NewConsumer(data)
		masterKey := MasterKey{}

		if err := fc.GenerateStruct(&masterKey); err != nil {
			return
		}

		_ = masterKey.Encrypt(data)
		_ = masterKey.EncryptIfNeeded(data)
	})
}
