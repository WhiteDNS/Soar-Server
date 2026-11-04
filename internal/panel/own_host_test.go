package panel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnHostPortsNeedAUsablePublicIP(t *testing.T) {
	for raw, usable := range map[string]bool{
		"91.107.247.64": true, "": false, "10.0.0.5": false, "192.168.1.5": false, "100.64.0.9": false,
		"127.0.0.1": false, "169.254.1.1": false, "203.0.113.9": false, "2606:4700::1": false, "bad": false,
	} {
		if _, ok := usableOwnIP(raw); ok != usable {
			t.Fatal("wrong decision for public IP", raw, ok)
		}
	}
	s := testStore(t)
	cfg, _ := s.Egress()
	cfg.OwnHostPorts = "14756"
	if err := s.SaveEgress(cfg); err == nil {
		t.Fatal("own-host list saved without a public IP")
	}
	settings, _ := s.Settings()
	settings.PublicIP = "91.107.247.64"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Egress()
	cfg.OwnHostPorts = "not-a-port"
	if err := s.SaveEgress(cfg); err == nil {
		t.Fatal("invalid own-host list saved")
	}
	cfg.OwnHostPorts = "14756,1000-1002"
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Egress()
	if !saved.ownHostAllows(14756) || !saved.ownHostAllows(1001) || saved.ownHostAllows(22) {
		t.Fatal("own-host list parsed wrongly", saved.OwnHostPorts)
	}
	if saved.publicView().OwnHostPorts != "14756,1000-1002" {
		t.Fatal("own-host list missing from the settings view")
	}
}

func TestRestoredBackupCannotEnableOwnHostPorts(t *testing.T) {
	dir := t.TempDir()
	source := testStore(t)
	settings, _ := source.Settings()
	settings.PublicIP = "91.107.247.64"
	if err := source.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	backupPath := func(name string) string {
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err = source.Backup(file); err != nil {
			t.Fatal(err)
		}
		return path
	}
	current := backupPath("current.db")
	cfg, _ := source.Egress()
	cfg.OwnHostPorts = "22"
	if err := source.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	candidate := backupPath("candidate.db")
	if err := PreserveRestoreAdmin(candidate, current); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(candidate)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Egress()
	if err != nil || got.OwnHostPorts != "" {
		t.Fatal("restored backup enabled own-host access", got.OwnHostPorts, err)
	}
}
