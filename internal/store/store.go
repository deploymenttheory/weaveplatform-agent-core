// Package store is core's durable, encrypted key/value store: one bbolt
// file owned by core, one bucket per module namespace, every value sealed
// with AES-256-GCM under a master key the platform protector guards.
// Modules cannot name, let alone read, each other's namespace — the
// namespace argument is bound at the hostserv seam, not chosen by callers.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/weaveplatform-agent/internal/store/keyprotect"
	bolt "go.etcd.io/bbolt"
)

// Store implements hostserv.StoreBackend with encryption at rest.
type Store struct {
	db   *bolt.DB
	aead cipher.AEAD
}

// Open opens (creating if needed) the store at dir/store.db, with the
// master key at dir/store.key sealed by the protector.
func Open(dir string, protector keyprotect.Protector) (*Store, error) {
	key, err := loadOrCreateKey(filepath.Join(dir, "store.key"), protector)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(dir, "store.db"), 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Store{db: db, aead: aead}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func loadOrCreateKey(path string, protector keyprotect.Protector) ([]byte, error) {
	if sealed, err := os.ReadFile(path); err == nil {
		key, err := protector.Unseal(sealed)
		if err != nil {
			return nil, fmt.Errorf("store: unsealing master key: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("store: master key has wrong length")
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	sealed, err := protector.Seal(key)
	if err != nil {
		return nil, fmt.Errorf("store: sealing master key: %w", err)
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// seal encrypts value binding it to namespace and key: a ciphertext moved
// to another key or namespace fails to open.
func (s *Store) seal(module, key string, value []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, value, aad(module, key)), nil
}

func (s *Store) open(module, key string, sealed []byte) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(sealed) < ns {
		return nil, fmt.Errorf("store: corrupt value")
	}
	return s.aead.Open(nil, sealed[:ns], sealed[ns:], aad(module, key))
}

func aad(module, key string) []byte {
	return []byte(module + "\x00" + key)
}

// Get implements hostserv.StoreBackend.
func (s *Store) Get(module, key string) ([]byte, bool, error) {
	var out []byte
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(module))
		if b == nil {
			return nil
		}
		sealed := b.Get([]byte(key))
		if sealed == nil {
			return nil
		}
		plain, err := s.open(module, key, sealed)
		if err != nil {
			return err
		}
		out = plain
		found = true
		return nil
	})
	return out, found, err
}

// Put implements hostserv.StoreBackend.
func (s *Store) Put(module, key string, value []byte) error {
	sealed, err := s.seal(module, key, value)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(module))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), sealed)
	})
}

// Delete implements hostserv.StoreBackend.
func (s *Store) Delete(module, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(module))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

// List implements hostserv.StoreBackend.
func (s *Store) List(module, prefix string) ([]string, error) {
	var keys []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(module))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if strings.HasPrefix(string(k), prefix) {
				keys = append(keys, string(k))
			}
			return nil
		})
	})
	return keys, err
}
