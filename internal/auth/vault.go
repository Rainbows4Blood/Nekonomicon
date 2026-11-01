package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// Vault keeps ciphertext outside catalog data; its encryption key lives in the OS
// credential manager (Windows Credential Manager / Linux Secret Service).
type Vault struct{ Dir string }

func (v Vault) aead(create bool) (cipher.AEAD, error) {
	text, e := keyring.Get("Nekonomicon", "dtrpg-cookie-key")
	if errors.Is(e, keyring.ErrNotFound) && create {
		b := make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		text = base64.StdEncoding.EncodeToString(b)
		clear(b)
		e = keyring.Set("Nekonomicon", "dtrpg-cookie-key", text)
	}
	if e != nil {
		return nil, errors.New("OS credential storage unavailable; unlock your keyring and retry")
	}
	b, e := base64.StdEncoding.DecodeString(text)
	if e != nil {
		return nil, e
	}
	defer clear(b)
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func (v Vault) Save(c Cookies) error {
	a, e := v.aead(true)
	if e != nil {
		return e
	}
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	defer clear(b)
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	encrypted := a.Seal(nonce, nonce, b, []byte("Nekonomicon DTRPG v1"))
	if e = os.MkdirAll(v.Dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(v.Dir, ".session-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(encrypted)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(v.Dir, "session.enc"))
}
func (v Vault) Load() (Cookies, error) {
	b, e := os.ReadFile(filepath.Join(v.Dir, "session.enc"))
	if e != nil {
		return Cookies{}, e
	}
	if len(b) > 2*Limit {
		return Cookies{}, errors.New("Invalid saved session")
	}
	a, e := v.aead(false)
	if e != nil {
		return Cookies{}, e
	}
	if len(b) < a.NonceSize() {
		return Cookies{}, errors.New("Invalid saved session")
	}
	p, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("Nekonomicon DTRPG v1"))
	if e != nil {
		return Cookies{}, errors.New("Saved session could not be decrypted")
	}
	defer clear(p)
	var c Cookies
	e = json.Unmarshal(p, &c)
	return c, e
}
