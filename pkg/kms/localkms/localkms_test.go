/*
 Copyright SecureKey Technologies Inc. All Rights Reserved.

 SPDX-License-Identifier: Apache-2.0
*/

package localkms

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/google/tink/go/keyset"
	"github.com/google/tink/go/subtle/random"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/aries-framework-go/pkg/kms"
	"github.com/hyperledger/aries-framework-go/pkg/kms/localkms/internal/keywrapper"
	mocksecretlock "github.com/hyperledger/aries-framework-go/pkg/mock/secretlock"
	mockstorage "github.com/hyperledger/aries-framework-go/pkg/mock/storage"
	"github.com/hyperledger/aries-framework-go/pkg/secretlock"
	"github.com/hyperledger/aries-framework-go/pkg/secretlock/local"
	"github.com/hyperledger/aries-framework-go/pkg/secretlock/local/masterlock/hkdf"
	"github.com/hyperledger/aries-framework-go/pkg/storage"
)

const testMasterKeyURI = keywrapper.LocalKeyURIPrefix + "test/key/uri"

func TestNewKMS_Failure(t *testing.T) {
	t.Run("test New() fail without masterkeyURI", func(t *testing.T) {
		kmsStorage, err := New("", &mockProvider{
			storage: mockstorage.NewMockStoreProvider(),
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.Error(t, err)
		require.Empty(t, kmsStorage)
	})

	t.Run("test New() fail due to error opening store", func(t *testing.T) {
		kmsStorage, err := New(testMasterKeyURI, &mockProvider{
			storage: &mockstorage.MockStoreProvider{
				ErrOpenStoreHandle: fmt.Errorf("failed to create store"),
			},
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.Error(t, err)
		require.Empty(t, kmsStorage)
	})

	t.Run("test New() error creating new KMS client with bad master key prefix", func(t *testing.T) {
		badKeyURI := "bad-prefix://test/key/uri"

		kmsStorage, err := New(badKeyURI, &mockProvider{
			storage: mockstorage.NewMockStoreProvider(),
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.Error(t, err)
		require.Empty(t, kmsStorage)
	})
}

func TestCreateGetRotateKey_Failure(t *testing.T) {
	t.Run("test failure Create() and Rotate() calls with bad key template string", func(t *testing.T) {
		kmsStorage, err := New(testMasterKeyURI, &mockProvider{
			storage: mockstorage.NewMockStoreProvider(),
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, kmsStorage)

		id, kh, err := kmsStorage.Create("")
		require.Error(t, err)
		require.Empty(t, kh)
		require.Empty(t, id)

		id, kh, err = kmsStorage.Create("unsupported")
		require.Error(t, err)
		require.Empty(t, kh)
		require.Empty(t, id)

		// create a valid key to test Rotate()
		id, kh, err = kmsStorage.Create("AES128GCM")
		require.NoError(t, err)
		require.NotEmpty(t, kh)
		require.NotEmpty(t, id)

		newID, kh, err := kmsStorage.Rotate("", id)
		require.Error(t, err)
		require.Empty(t, kh)
		require.Empty(t, newID)

		newID, kh, err = kmsStorage.Rotate("unsupported", id)
		require.Error(t, err)
		require.Empty(t, kh)
		require.Empty(t, newID)
	})

	t.Run("test Create() with failure to store key", func(t *testing.T) {
		kmsStorage, err := New(testMasterKeyURI, &mockProvider{
			storage: &mockstorage.MockStoreProvider{
				Store: &mockstorage.MockStore{
					ErrPut: fmt.Errorf("failed to put data")},
			},
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.NoError(t, err)

		id, kh, err := kmsStorage.Create("AES128GCM")
		require.EqualError(t, err, "failed to put data")
		require.Empty(t, kh)
		require.Empty(t, id)
	})

	t.Run("test Create() success to store key but fail to get key from store", func(t *testing.T) {
		storeData := map[string][]byte{}
		kmsStorage, err := New(testMasterKeyURI, &mockProvider{
			storage: &mockstorage.MockStoreProvider{
				Store: &mockstorage.MockStore{
					Store: storeData,
				},
			},
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.NoError(t, err)

		id, kh, err := kmsStorage.Create("AES128GCM")
		require.NoError(t, err)
		require.NotEmpty(t, kh)
		require.NotEmpty(t, id)

		// new create a new client with a store throwing an error during a Get()
		kmsStorage3, err := New(testMasterKeyURI, &mockProvider{
			storage: &mockstorage.MockStoreProvider{
				Store: &mockstorage.MockStore{
					ErrGet: fmt.Errorf("failed to get data"),
					Store:  storeData,
				},
			},
			secretLock: &mocksecretlock.MockSecretLock{
				ValEncrypt: "",
				ValDecrypt: "",
			},
		})
		require.NoError(t, err)

		kh, err = kmsStorage3.Get(id)
		require.Contains(t, err.Error(), "failed to get data")
		require.Empty(t, kh)

		newID, kh, err := kmsStorage3.Rotate("AES128GCM", id)
		require.Contains(t, err.Error(), "failed to get data")
		require.Empty(t, kh)
		require.Empty(t, newID)
	})
}

func TestLocalKMS_Success(t *testing.T) {
	// create a real (not mocked) master key and secret lock to test the KMS end to end
	sl := createMasterKeyAndSecretLock(t)

	storeDB := make(map[string][]byte)
	// test New()
	kmsService, err := New(testMasterKeyURI, &mockProvider{
		storage: mockstorage.NewCustomMockStoreProvider(
			&mockstorage.MockStore{
				Store: storeDB,
			}),
		secretLock: sl,
	})
	require.NoError(t, err)
	require.NotEmpty(t, kmsService)

	keyTemplates := []kms.KeyType{
		kms.AES128GCMType,
		kms.AES256GCMNoPrefixType,
		kms.AES256GCMType,
		kms.ChaCha20Poly1305Type,
		kms.XChaCha20Poly1305Type,
		kms.ECDSAP256Type,
		kms.ECDSAP384Type,
		kms.ECDSAP521Type,
		kms.ED25519Type,
	}

	for _, v := range keyTemplates {
		// test Create() a new key
		keyID, newKeyHandle, e := kmsService.Create(v)
		require.NoError(t, e)
		require.NotEmpty(t, newKeyHandle)
		require.NotEmpty(t, keyID)
		ks, ok := storeDB[keyID]
		require.True(t, ok)
		require.NotEmpty(t, ks)

		// get key handle primitives
		newKHPrimitives, e := newKeyHandle.(*keyset.Handle).Primitives()
		require.NoError(t, e)
		require.NotEmpty(t, newKHPrimitives)

		// test Get() an existing keyhandle (it should match newKeyHandle above)
		loadedKeyHandle, e := kmsService.Get(keyID)
		require.NoError(t, e)
		require.NotEmpty(t, loadedKeyHandle)

		readKHPrimitives, e := loadedKeyHandle.(*keyset.Handle).Primitives()
		require.NoError(t, e)
		require.NotEmpty(t, newKHPrimitives)

		require.Equal(t, len(newKHPrimitives.Entries), len(readKHPrimitives.Entries))

		// finally test Rotate()
		// with unsupported key type - should fail
		newKeyID, rotatedKeyHandle, e := kmsService.Rotate("unsupported", keyID)
		require.Error(t, e)
		require.Empty(t, rotatedKeyHandle)
		require.Empty(t, newKeyID)

		// with valid key type - should succeed
		newKeyID, rotatedKeyHandle, e = kmsService.Rotate(v, keyID)
		require.NoError(t, e)
		require.NotEmpty(t, rotatedKeyHandle)
		require.NotEqual(t, newKeyID, keyID)

		rotatedKHPrimitives, e := loadedKeyHandle.(*keyset.Handle).Primitives()
		require.NoError(t, e)
		require.NotEmpty(t, newKHPrimitives)
		require.Equal(t, len(newKHPrimitives.Entries), len(rotatedKHPrimitives.Entries))
		require.Equal(t, len(readKHPrimitives.Entries), len(rotatedKHPrimitives.Entries))

		if strings.Contains(string(v), "ECDSA") || v == kms.ED25519Type {
			pubKeyBytes, e := kmsService.ExportPubKeyBytes(keyID)
			require.Errorf(t, e, "KeyID has been rotated. An error must be returned")
			require.Empty(t, pubKeyBytes)

			pubKeyBytes, e = kmsService.ExportPubKeyBytes(newKeyID)
			require.NoError(t, e)
			require.NotEmpty(t, pubKeyBytes)

			kh, e := kmsService.PubKeyBytesToHandle(pubKeyBytes, v)
			require.NoError(t, e)
			require.NotEmpty(t, kh)
		}
	}
}

func TestLocalKMS_getKeyTemplate(t *testing.T) {
	keyTemplate, err := getKeyTemplate(kms.HMACSHA256Tag256Type)
	require.NoError(t, err)
	require.NotNil(t, keyTemplate)
	require.Equal(t, "type.googleapis.com/google.crypto.tink.HmacKey", keyTemplate.TypeUrl)
}

func createMasterKeyAndSecretLock(t *testing.T) secretlock.Service {
	t.Helper()

	masterKeyFilePath := "masterKey_file.txt"
	tmpfile, err := ioutil.TempFile("", masterKeyFilePath)
	require.NoError(t, err)

	defer func() {
		// close file
		require.NoError(t, tmpfile.Close())
		// clean up file
		require.NoError(t, os.Remove(tmpfile.Name()))
	}()

	masterKeyContent := random.GetRandomBytes(uint32(32))
	require.NotEmpty(t, masterKeyContent)

	// first create a master lock to use in our secret lock and encrypt the master key
	passphrase := "secretPassphrase"
	keySize := sha256.Size
	// salt is optional, it can be nil
	salt := make([]byte, keySize)
	_, err = rand.Read(salt)
	require.NoError(t, err)

	masterLocker, err := hkdf.NewMasterLock(passphrase, sha256.New, salt)
	require.NoError(t, err)
	require.NotEmpty(t, masterLocker)

	// now encrypt masterKeyContent
	masterLockEnc, err := masterLocker.Encrypt("", &secretlock.EncryptRequest{
		Plaintext: string(masterKeyContent)})
	require.NoError(t, err)
	require.NotEmpty(t, masterLockEnc)

	// and write it to tmpfile
	n, err := tmpfile.Write([]byte(masterLockEnc.Ciphertext))
	require.NoError(t, err)
	require.Equal(t, len(masterLockEnc.Ciphertext), n)

	// now get a reader from path
	r, err := local.MasterKeyFromPath(tmpfile.Name())
	require.NoError(t, err)
	require.NotEmpty(t, r)

	// finally create lock service with the master lock created above to encrypt decrypt keys using
	// a protected (encrypted) master key
	s, err := local.NewService(r, masterLocker)
	require.NoError(t, err)
	require.NotEmpty(t, s)

	return s
}

// mockProvider mocks a provider for KMS storage
type mockProvider struct {
	storage    *mockstorage.MockStoreProvider
	secretLock secretlock.Service
}

func (m *mockProvider) StorageProvider() storage.Provider {
	return m.storage
}

func (m *mockProvider) SecretLock() secretlock.Service {
	return m.secretLock
}
