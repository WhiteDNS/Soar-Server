package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIInitializesAndProtectsManagementBind(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "panel.db")
	pw := filepath.Join(dir, "password")
	if e := os.WriteFile(pw, []byte("test-only-password-2026\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", pw}); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", pw}); e == nil {
		t.Fatal("existing database overwritten")
	}
	if e := execute([]string{"run", "--db", db, "--listen", "0.0.0.0:2053"}); e == nil {
		t.Fatal("public plaintext admin accepted")
	}
	if e := execute([]string{"backup", "--db", db, "--output", filepath.Join(dir, "backup.db")}); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"admin-reset", "--db", db, "--password-file", pw}); e != nil {
		t.Fatal(e)
	}
}
