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

// MasterKey is an age key used to encrypt and decrypt sops' data key.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.0/age/keysource.go
// to be able to have fine-grain control over the used decryption keys
// without relying on the existence of file(path)s.
type MasterKey struct {
	Identities   []string // a slice of Bech32-encoded private keys
	Recipient    string   // a Bech32-encoded public key
	EncryptedKey string   // a sops data key encrypted with age

	parsedIdentities []age.Identity       // a slice of parsed age private keys
	parsedRecipient  *age.X25519Recipient // a parsed age public key
}

// Encrypt takes a sops data key, encrypts it with age and stores the result in the EncryptedKey field.
func (key *MasterKey) Encrypt(datakey []byte) error {
	buffer := &bytes.Buffer{}

	if key.parsedRecipient == nil {
		parsedRecipient, err := parseRecipient(key.Recipient)

		if err != nil {
			return err
		}

		key.parsedRecipient = parsedRecipient
	}

	aw := armor.NewWriter(buffer)
	w, err := age.Encrypt(aw, key.parsedRecipient)
	if err != nil {
		return fmt.Errorf("failed to open file for encrypting sops data key with age: %w", err)
	}

	if _, err := w.Write(datakey); err != nil {
		return fmt.Errorf("failed to encrypt sops data key with age: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close file for encrypting sops data key with age: %w", err)
	}

	if err := aw.Close(); err != nil {
		return fmt.Errorf("failed to close armored writer: %w", err)
	}

	key.EncryptedKey = buffer.String()
	return nil
}

// EncryptIfNeeded encrypts the provided sops' data key and encrypts it if it hasn't been encrypted yet.
func (key *MasterKey) EncryptIfNeeded(datakey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(datakey)
	}

	return nil
}

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Decrypt decrypts the EncryptedKey field with the age identity and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	if len(key.parsedIdentities) <= 0 && len(key.Identities) > 0 {
		var identities []age.Identity
		for _, i := range key.Identities {
			i, err := age.ParseIdentities(strings.NewReader(i))
			if err != nil {
				return nil, err
			}
			identities = append(identities, i...)
		}
		key.parsedIdentities = identities
	}

	src := bytes.NewReader([]byte(key.EncryptedKey))
	ar := armor.NewReader(src)
	r, err := age.Decrypt(ar, key.parsedIdentities...)

	if err != nil {
		return nil, fmt.Errorf("no age identity found that could decrypt the data")
	}

	var b bytes.Buffer
	if _, err := io.Copy(&b, r); err != nil {
		return nil, fmt.Errorf("failed to copy decrypted data into bytes.Buffer: %w", err)
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
	return map[string]interface{}{"recipient": key.Recipient, "enc": key.EncryptedKey}
}

// MasterKeysFromRecipients takes a comma-separated list of Bech32-encoded public keys and returns a
// slice of new MasterKeys.
func MasterKeysFromRecipients(commaSeparatedRecipients string) ([]*MasterKey, error) {
	if commaSeparatedRecipients == "" {
		// otherwise Split returns [""] and MasterKeyFromRecipient is unhappy
		return make([]*MasterKey, 0), nil
	}
	recipients := strings.Split(commaSeparatedRecipients, ",")

	var keys []*MasterKey

	for _, recipient := range recipients {
		key, err := MasterKeyFromRecipient(recipient)

		if err != nil {
			return nil, err
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// MasterKeyFromRecipient takes a Bech32-encoded public key and returns a new MasterKey.
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

// parseRecipient attempts to parse a string containing an encoded age public key
func parseRecipient(recipient string) (*age.X25519Recipient, error) {
	parsedRecipient, err := age.ParseX25519Recipient(recipient)

	if err != nil {
		return nil, fmt.Errorf("failed to parse input as Bech32-encoded age public key: %w", err)
	}

	return parsedRecipient, nil
}
