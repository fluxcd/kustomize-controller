// Copyright (C) 2021 The Mozilla SOPS authors
// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package age

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// MasterKey is an age key used to Encrypt and Decrypt SOPS' data key.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.2/age/keysource.go
// to be able to have fine-grain control over the used decryption keys
// without relying on the existence of file(path)s.
type MasterKey struct {
	// Identities contains the set of Bench32-encoded age identities used to
	// Decrypt.
	// They are lazy-loaded using MasterKeyFromIdentities, or on first
	// Decrypt().
	// In addition to using this field, ParsedIdentities.ApplyToMasterKey() can
	// be used to parse and lazy-load identities.
	Identities []string
	// Recipient contains the Bench32-encoded age public key used to Encrypt.
	Recipient string
	// EncryptedKey contains the SOPS data key encrypted with age.
	EncryptedKey string

	// parsedIdentities contains a slice of parsed age identities.
	// It is used to lazy-load the Identities at-most once.
	// It can also be injected by a (local) keyservice.KeyServiceServer using
	// ParsedIdentities.ApplyToMasterKey().
	parsedIdentities []age.Identity
	// parsedRecipient contains a parsed age public key.
	// It is used to lazy-load the Recipient at-most once.
	parsedRecipient *age.X25519Recipient
}

// MasterKeyFromRecipient takes a Bech32-encoded age public key, parses it, and
// returns a new MasterKey.
func MasterKeyFromRecipient(recipient string) (*MasterKey, error) {
	parsedRecipient, err := parseRecipient(recipient)
	if err != nil {
		return nil, err
	}
	return &MasterKey{
		Recipient:       recipient,
		parsedRecipient: parsedRecipient,
	}, nil
}

// MasterKeyFromIdentities takes a set if Bech32-encoded age identities, parses
// them, and returns a new MasterKey.
func MasterKeyFromIdentities(identities ...string) (*MasterKey, error) {
	parsedIdentities, err := parseIdentities(identities...)
	if err != nil {
		return nil, err
	}
	return &MasterKey{
		Identities:       identities,
		parsedIdentities: parsedIdentities,
	}, nil
}

// ParsedIdentities contains a set of parsed age identities.
// It allows for creating a (local) keyservice.KeyServiceServer which parses
// identities only once, to then inject them using ApplyToMasterKey() for all
// requests.
type ParsedIdentities []age.Identity

// Import attempts to parse the given identities, to then add them to itself.
// It returns any parsing error.
// A single identity argument is allowed to be a multiline string containing
// multiple identities. Empty lines and lines starting with "#" are ignored.
// It is not thread safe, and parallel importing would better be done by
// parsing (using age.ParseIdentities) and appending to the slice yourself, in
// combination with e.g. a sync.Mutex.
func (i *ParsedIdentities) Import(identity ...string) error {
	identities, err := parseIdentities(identity...)
	if err != nil {
		return fmt.Errorf("failed to parse and add to age identities: %w", err)
	}
	*i = append(*i, identities...)
	return nil
}

// ApplyToMasterKey configures the ParsedIdentities on the provided key.
func (i ParsedIdentities) ApplyToMasterKey(key *MasterKey) {
	key.parsedIdentities = i
}

// Encrypt takes a SOPS data key, encrypts it with the Recipient, and stores
// the result in the EncryptedKey field.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	if key.parsedRecipient == nil {
		parsedRecipient, err := parseRecipient(key.Recipient)
		if err != nil {
			return err
		}
		key.parsedRecipient = parsedRecipient
	}

	var buffer bytes.Buffer
	aw := armor.NewWriter(&buffer)
	w, err := age.Encrypt(aw, key.parsedRecipient)
	if err != nil {
		return fmt.Errorf("failed to create writer for encrypting sops data key with age: %w", err)
	}
	if _, err := w.Write(dataKey); err != nil {
		return fmt.Errorf("failed to encrypt sops data key with age: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close writer for encrypting sops data key with age: %w", err)
	}
	if err := aw.Close(); err != nil {
		return fmt.Errorf("failed to close armored writer: %w", err)
	}

	key.SetEncryptedDataKey(buffer.Bytes())
	return nil
}

// EncryptIfNeeded encrypts the provided SOPS data key, if it has not been
// encrypted yet.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// EncryptedDataKey returns the encrypted SOPS data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted SOPS data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Decrypt decrypts the EncryptedKey with the (parsed) Identities and returns
// the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	if len(key.parsedIdentities) == 0 && len(key.Identities) > 0 {
		parsedIdentities, err := parseIdentities(key.Identities...)
		if err != nil {
			return nil, err
		}
		key.parsedIdentities = parsedIdentities
	}

	src := bytes.NewReader([]byte(key.EncryptedKey))
	ar := armor.NewReader(src)
	r, err := age.Decrypt(ar, key.parsedIdentities...)
	if err != nil {
		return nil, fmt.Errorf("failed to create reader for decrypting sops data key with age: %w", err)
	}

	var b bytes.Buffer
	if _, err := io.Copy(&b, r); err != nil {
		return nil, fmt.Errorf("failed to copy age decrypted data into bytes.Buffer: %w", err)
	}
	return b.Bytes(), nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return false
}

// ToString converts the key to a string representation.
func (key *MasterKey) ToString() string {
	return key.Recipient
}

// ToMap converts the MasterKey to a map for serialization purposes.
func (key *MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["recipient"] = key.Recipient
	out["enc"] = key.EncryptedKey
	return out
}

// parseRecipient attempts to parse a string containing an encoded age public
// key.
func parseRecipient(recipient string) (*age.X25519Recipient, error) {
	parsedRecipient, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("failed to parse input as Bech32-encoded age public key: %w", err)
	}
	return parsedRecipient, nil
}

// parseIdentities attempts to parse the string set of encoded age identities.
// A single identity argument is allowed to be a multiline string containing
// multiple identities. Empty lines and lines starting with "#" are ignored.
func parseIdentities(identity ...string) ([]age.Identity, error) {
	var identities []age.Identity
	for _, i := range identity {
		parsed, err := age.ParseIdentities(strings.NewReader(i))
		if err != nil {
			return nil, err
		}
		identities = append(identities, parsed...)
	}
	return identities, nil
}
