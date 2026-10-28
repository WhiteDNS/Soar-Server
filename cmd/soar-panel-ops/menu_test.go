package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	managementassets "soar-server-setup"
)

func TestEmbeddedMenuUpgradesOlderInstallationAndPreservesAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix file replacement and symlinks; checked on Linux CI")
	}
	dir := t.TempDir()
	menu, alias := filepath.Join(dir, "soar-panel-menu"), filepath.Join(dir, "whitedns")
	if err := installManagementMenu(menu, alias); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(menu)
	info, _ := os.Stat(menu)
	if !bytes.Equal(raw, managementassets.MenuScript) || info.Mode().Perm() != 0755 {
		t.Fatal("missing or non-executable embedded menu")
	}
	_ = os.Remove(alias)
	_ = os.WriteFile(alias, []byte("unrelated command"), 0600)
	if err := installManagementMenu(menu, alias); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(alias)
	if string(raw) != "unrelated command" {
		t.Fatal("overwrote an existing command")
	}
}
