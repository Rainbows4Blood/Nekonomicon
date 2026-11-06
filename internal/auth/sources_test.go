package auth

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestStorefrontCookieIsolation(t *testing.T) {
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		c := Cookies{Imported: []Cookie{
			{Name: "session", Value: "synthetic", Domain: "." + source.Domain(), Path: "/", Secure: true, FirstParty: source.Site()},
		}}
		for _, target := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
			u, _ := url.Parse(target.API() + "api/vBeta/products/1")
			if got := c.Header(u, target); (got != "") != (source == target) {
				t.Fatal("imported cookie crossed storefronts")
			}
			_, _, err := Context("^partitionKey=%28https%2C"+source.Domain()+"%29", target)
			if (err == nil) != (source == target) {
				t.Fatal("foreign partition accepted")
			}
		}
		manual, err := Manual("web=synthetic", "api=synthetic", source)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{source.Site() + "/", "https://d1vzi28wh99zvq.cloudfront.net/images/1.png", "https://www." + source.Domain() + ".invalid/"} {
			u, _ := url.Parse(raw)
			if manual.Header(u, source) != "" {
				t.Fatal("manual header escaped exact host")
			}
		}
	}
}

func TestFirefoxImportsSelectedStoreOnly(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "cookies.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE moz_cookies(name,value,host,path,isSecure,expiry,originAttributes,creationTime);
INSERT INTO moz_cookies VALUES('session','synthetic','.drivethrurpg.com','/',1,4102444800,'',1),
('session','synthetic','.dmsguild.com','/',1,4102444800,'^partitionKey=%28https%2Cdmsguild.com%29',2),
('foreign','synthetic','.dmsguild.com','/',1,4102444800,'^partitionKey=%28https%2Cdrivethrurpg.com%29',3);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		c, _, err := Import(Profile{Path: dir}, 0, source)
		if err != nil || len(c.Imported) != 1 || c.Imported[0].Domain != "."+source.Domain() {
			t.Fatal("incorrect selected-store import", err)
		}
	}
}
