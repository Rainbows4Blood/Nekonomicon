package auth

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestFirefoxReadOnlySnapshot(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "cookies.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA user_version=16; CREATE TABLE moz_cookies(name,value,host,path,isSecure,expiry,originAttributes,creationTime); INSERT INTO moz_cookies VALUES('session','synthetic','.drivethrurpg.com','/',1,4102444800000,'',1),('foreign','synthetic','.unrelated.invalid','/',1,4102444800000,'',2),('container','synthetic','.drivethrurpg.com','/',1,4102444800000,'^userContextId=2',3);`)
	if e != nil {
		t.Fatal(e)
	}
	c, _, e := Import(Profile{Path: dir}, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Imported) != 1 || c.Imported[0].Expires != 4102444800 {
		t.Fatal("scope or millisecond expiry not preserved")
	}
	if e = os.WriteFile(filepath.Join(dir, "profiles.ini"), []byte("[Profile0]\nName=test\nIsRelative=0\nPath="+dir+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if len(ProfilesAt(dir)) != 1 {
		t.Fatal("profile discovery")
	}
}
