// Package openaiauth owns the desktop ChatGPT OAuth connection. Tokens never
// cross the Wails bridge or enter the catalog database.
package openaiauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

type account struct {
	ClientID, Subject, Email, Name            string
	AccessToken, RefreshToken, IDToken, Scope string
	ExpiresAt                                 time.Time
}
type credentials struct {
	HostID   string
	Active   string
	Accounts []account
}
type diskStorage struct {
	dir    string
	getKey func() (string, error)
	setKey func(string) error
}

func newDiskStorage(dir string) *diskStorage {
	return &diskStorage{dir: dir,
		getKey: func() (string, error) { return keyring.Get("Nekonomicon", "openai-key") },
		setKey: func(s string) error { return keyring.Set("Nekonomicon", "openai-key", s) },
	}
}
func (s *diskStorage) aead(create bool) (cipher.AEAD, error) {
	key, err := s.getKey()
	if errors.Is(err, keyring.ErrNotFound) && create {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		key = base64.StdEncoding.EncodeToString(b)
		clear(b)
		err = s.setKey(key)
	}
	if err != nil {
		return nil, errors.New("Unlock the OS credential store and retry OpenAI sign-in")
	}
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, errors.New("OpenAI credential key is invalid")
	}
	defer clear(b)
	block, err := aes.NewCipher(b)
	if err != nil {
		return nil, errors.New("OpenAI credential key is invalid")
	}
	return cipher.NewGCM(block)
}
func (s *diskStorage) load() (credentials, error) {
	var c credentials
	f, err := os.Open(filepath.Join(s.dir, "openai.enc"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, errors.New("Could not read saved OpenAI connection")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return c, errors.New("Saved OpenAI connection is invalid")
	}
	a, err := s.aead(false)
	if err != nil {
		return c, err
	}
	if len(b) < a.NonceSize() {
		return c, errors.New("Saved OpenAI connection is invalid")
	}
	plain, err := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("Nekonomicon OpenAI v1"))
	if err != nil {
		return c, errors.New("Could not decrypt saved OpenAI connection")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &c) != nil || len(c.Accounts) > 20 {
		return credentials{}, errors.New("Saved OpenAI connection is invalid")
	}
	return c, nil
}
func (s *diskStorage) save(c credentials) error {
	a, err := s.aead(true)
	if err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil || len(b) > 1<<20 {
		return errors.New("OpenAI connection is too large to save")
	}
	defer clear(b)
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	encrypted := a.Seal(nonce, nonce, b, []byte("Nekonomicon OpenAI v1"))
	f, err := os.CreateTemp(s.dir, ".openai-*")
	if err != nil {
		return errors.New("Could not save OpenAI connection")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(encrypted)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(s.dir, "openai.enc"))
	}
	if err != nil {
		return errors.New("Could not save OpenAI connection")
	}
	return nil
}
