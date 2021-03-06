/*
 Copyright SecureKey Technologies Inc. All Rights Reserved.

 SPDX-License-Identifier: Apache-2.0
*/

package localkms

import (
	"bytes"
	"fmt"

	"github.com/google/tink/go/aead"
	"github.com/google/tink/go/keyset"
	"github.com/google/tink/go/mac"
	tinkpb "github.com/google/tink/go/proto/tink_go_proto"
	"github.com/google/tink/go/signature"

	"github.com/hyperledger/aries-framework-go/pkg/kms"
	"github.com/hyperledger/aries-framework-go/pkg/kms/localkms/internal/keywrapper"
	"github.com/hyperledger/aries-framework-go/pkg/secretlock"
	"github.com/hyperledger/aries-framework-go/pkg/storage"
)

const (
	// Namespace is the keystore's DB storage namespace
	Namespace = "kmsdb"
)

// LocalKMS implements kms.KeyManager to provide key management capabilities using a local db.
// It uses an underlying secret lock service (default local secretLock) to wrap (encrypt) keys
// prior to storing them.
type LocalKMS struct {
	secretLock       secretlock.Service
	masterKeyURI     string
	store            storage.Store
	masterKeyEnvAEAD *aead.KMSEnvelopeAEAD
}

// New will create a new (local) KMS service
func New(masterKeyURI string, p kms.Provider) (*LocalKMS, error) {
	store, err := p.StorageProvider().OpenStore(Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to ceate local kms: %w", err)
	}

	secretLock := p.SecretLock()

	kw, err := keywrapper.New(secretLock, masterKeyURI)
	if err != nil {
		return nil, err
	}

	// create a KMSEnvelopeAEAD instance to wrap/unwrap keys managed by LocalKMS
	masterKeyEnvAEAD := aead.NewKMSEnvelopeAEAD(*aead.AES256GCMKeyTemplate(), kw)

	return &LocalKMS{
			store:            store,
			secretLock:       secretLock,
			masterKeyURI:     masterKeyURI,
			masterKeyEnvAEAD: masterKeyEnvAEAD},
		nil
}

// Create a new key/keyset for key type kt, store it and return its stored ID and key handle
func (l *LocalKMS) Create(kt kms.KeyType) (string, interface{}, error) {
	if kt == "" {
		return "", nil, fmt.Errorf("failed to create new key, missing key type")
	}

	keyTemplate, err := getKeyTemplate(kt)
	if err != nil {
		return "", nil, err
	}

	kh, err := keyset.NewHandle(keyTemplate)
	if err != nil {
		return "", nil, err
	}

	kID, err := l.storeKeySet(kh)
	if err != nil {
		return "", nil, err
	}

	return kID, kh, nil
}

// Get key handle for the given keyID
func (l *LocalKMS) Get(keyID string) (interface{}, error) {
	return l.getKeySet(keyID)
}

// Rotate a key referenced by keyID and return its updated handle
func (l *LocalKMS) Rotate(kt kms.KeyType, keyID string) (string, interface{}, error) {
	kh, err := l.getKeySet(keyID)
	if err != nil {
		return "", nil, err
	}

	keyTemplate, err := getKeyTemplate(kt)
	if err != nil {
		return "", nil, err
	}

	km := keyset.NewManagerFromHandle(kh)

	err = km.Rotate(keyTemplate)
	if err != nil {
		return "", nil, err
	}

	updatedKH, err := km.Handle()

	if err != nil {
		return "", nil, err
	}

	err = l.store.Delete(keyID)
	if err != nil {
		return "", nil, err
	}

	newID, err := l.storeKeySet(updatedKH)
	if err != nil {
		return "", nil, err
	}

	return newID, updatedKH, nil
}

// nolint:gocyclo
func getKeyTemplate(keyType kms.KeyType) (*tinkpb.KeyTemplate, error) {
	switch keyType {
	case kms.AES128GCMType:
		return aead.AES128GCMKeyTemplate(), nil
	case kms.AES256GCMNoPrefixType:
		// RAW (to support keys not generated by Tink)
		return aead.AES256GCMNoPrefixKeyTemplate(), nil
	case kms.AES256GCMType:
		return aead.AES256GCMKeyTemplate(), nil
	case kms.ChaCha20Poly1305Type:
		return aead.ChaCha20Poly1305KeyTemplate(), nil
	case kms.XChaCha20Poly1305Type:
		return aead.XChaCha20Poly1305KeyTemplate(), nil
	case kms.ECDSAP256Type:
		return signature.ECDSAP256KeyWithoutPrefixTemplate(), nil
	case kms.ECDSAP384Type:
		return signature.ECDSAP384KeyWithoutPrefixTemplate(), nil
	case kms.ECDSAP521Type:
		return signature.ECDSAP521KeyWithoutPrefixTemplate(), nil
	case kms.ED25519Type:
		return signature.ED25519KeyWithoutPrefixTemplate(), nil
	case kms.HMACSHA256Tag256Type:
		return mac.HMACSHA256Tag256KeyTemplate(), nil
	default:
		return nil, fmt.Errorf("key type unrecognized")
	}
}

func (l *LocalKMS) storeKeySet(kh *keyset.Handle) (string, error) {
	w := newWriter(l.store, l.masterKeyURI)

	buf := new(bytes.Buffer)
	jsonKeysetWriter := keyset.NewJSONWriter(buf)

	err := kh.Write(jsonKeysetWriter, l.masterKeyEnvAEAD)
	if err != nil {
		return "", err
	}

	// write buffer to localstorage
	_, err = w.Write(buf.Bytes())
	if err != nil {
		return "", err
	}

	return w.KeysetID, nil
}

func (l *LocalKMS) getKeySet(id string) (*keyset.Handle, error) {
	localDBReader := newReader(l.store, id)
	jsonKeysetReader := keyset.NewJSONReader(localDBReader)

	// Read reads the encrypted keyset handle back from the io.reader implementation
	// and decrypts it using masterKeyEnvAEAD.
	kh, err := keyset.Read(jsonKeysetReader, l.masterKeyEnvAEAD)
	if err != nil {
		return nil, err
	}

	return kh, nil
}

// ExportPubKeyBytes will fetch a key referenced by id then gets its public key in raw bytes
// and returns it.
// The key must be an asymmetric key
// it returns an error if it fails to export the public key bytes
func (l *LocalKMS) ExportPubKeyBytes(id string) ([]byte, error) {
	kh, err := l.getKeySet(id)
	if err != nil {
		return nil, err
	}

	// kh must be a private asymmetric key in order to extract its public key
	pubKH, err := kh.Public()
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	pubKeyWriter := NewWriter(buf)

	err = pubKH.WriteWithNoSecrets(pubKeyWriter)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// PubKeyBytesToHandle will create and return a key handle for pubKey of type kt
// it returns an error if it failed creating the key handle
// Note: The key handle created is not stored in the KMS, it's only useful to execute the crypto primitive
// associated with it.
func (l *LocalKMS) PubKeyBytesToHandle(pubKey []byte, kt kms.KeyType) (*keyset.Handle, error) {
	return publicKeyBytesToHandle(pubKey, kt)
}
