package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
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

func TestVaultStorefrontIsolationIntegration(t *testing.T) {
	if os.Getenv("NEKONOMICON_KEYRING_TEST") != "1" {
		t.Skip("opt-in OS credential manager integration")
	}
	dir := t.TempDir()
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		v := Vault{Dir: dir, Source: source}
		c, err := Manual("session=synthetic-"+string(source), "", source)
		if err != nil {
			t.Fatal(err)
		}
		if err = v.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		v := Vault{Dir: dir, Source: source}
		c, err := v.Load()
		if err != nil || c.Raw[source.Website()] != "session=synthetic-"+string(source) {
			t.Fatal("vault crossed storefronts", err)
		}
	}
	legacy, err := os.ReadFile(filepath.Join(dir, "session.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "session-dmsguild.enc"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = (Vault{Dir: dir, Source: storefront.DMSGuild}).Load(); err == nil {
		t.Fatal("foreign session ciphertext accepted")
	}
}
