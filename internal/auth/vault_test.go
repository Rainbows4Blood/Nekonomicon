package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultIntegration(t *testing.T) {
	if os.Getenv("NEKONOMICON_KEYRING_TEST") != "1" {
		t.Skip("opt-in OS credential manager integration")
	}
	v := Vault{Dir: t.TempDir()}
	c, _ := Manual("session=synthetic-vault-test", "")
	if err := v.Save(c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(v.Dir, "session.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("synthetic-vault-test")) {
		t.Fatal("secret stored in plaintext")
	}
	loaded, err := v.Load()
	if err != nil || loaded.Raw[Website] != c.Raw[Website] {
		t.Fatal("credential round trip failed")
	}
	if err = v.Save(c); err != nil {
		t.Fatal("replacement failed", err)
	}
	b[len(b)-1] ^= 1
	os.WriteFile(filepath.Join(v.Dir, "session.enc"), b, 0600)
	if _, err = v.Load(); err == nil {
		t.Fatal("tampered credentials accepted")
	}
}
