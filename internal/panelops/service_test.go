package panelops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"soar-server-setup/internal/panel"
)

func database(t *testing.T, path, password string) {
	t.Helper()
	s, e := panel.OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Init(panel.Settings{BaseZone: "soar.example.org", Country: "IR", Resolvers: []string{"1.1.1.1"}, AllowPorts: []uint16{80, 443}}, "admin", password); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(panel.ClientInput{Name: password, Enabled: true, StreamLimit: 16}); e != nil {
		t.Fatal(e)
	}
}
func TestRestoreActivationRollbackAndPersistentRecovery(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[failStart], func(t *testing.T) {
			dir := t.TempDir()
			current := filepath.Join(dir, "panel.db")
			candidate := filepath.Join(dir, "candidate.db")
			database(t, current, "current-password-2026")
			telegramFile := filepath.Join(dir, "telegram.json")
			telegramConfig := []byte(`{"token":"private-server-bot","chat_id":12345}`)
			if e := os.WriteFile(telegramFile, telegramConfig, 0600); e != nil {
				t.Fatal(e)
			}
			database(t, candidate, "backup-password-2026")
			raw, _ := os.ReadFile(candidate)
			hash := sha256.Sum256(raw)
			binary := filepath.Join(dir, "soar-panel")
			ops := filepath.Join(dir, "soar-panel-ops")
			_ = os.WriteFile(binary, []byte("original-panel"), 0755)
			_ = os.WriteFile(ops, []byte("original-ops"), 0755)
			starts := 0
			s := &Service{DB: current, Binary: binary, OpsBinary: ops, StateDir: filepath.Join(dir, "jobs"), UID: -1, GID: -1, Inspect: func(_ context.Context, path string) (panel.BackupSummary, error) {
				return panel.InspectBackupFile(path)
			}, Control: func(_ context.Context, action string) error {
				if action == "start" {
					starts++
					if failStart && starts == 1 {
						return errors.New("fixture start failure")
					}
				}
				return nil
			}, Health: func(context.Context) error { return nil }}
			if e := s.Initialize(); e != nil {
				t.Fatal(e)
			}
			job := panel.MaintenanceJob{ID: "restore-fixture", Kind: "restore", State: "running"}
			s.perform(panel.MaintenanceRequest{Action: "restore", Backup: raw, SHA256: hex.EncodeToString(hash[:]), ExpectedZone: "soar.example.org"}, job)
			state := s.Status()
			if raw, e := os.ReadFile(telegramFile); e != nil || !bytes.Equal(raw, telegramConfig) {
				t.Fatal("restore/rollback changed machine-local Telegram access", e)
			}
			want := "succeeded"
			if failStart {
				want = "rolled_back"
			}
			if state.State != want {
				t.Fatal(state)
			}
			restored, e := panel.OpenStore(current)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			if !restored.CheckAdmin("admin", "current-password-2026") {
				t.Fatal("administrator changed")
			}
			list, _ := restored.List()
			name := "backup-password-2026"
			if failStart {
				name = "current-password-2026"
			}
			if len(list) != 1 || list[0].Name != name {
				t.Fatal("unexpected restored account set")
			}
			if _, e = os.Stat(filepath.Join(state.Backup, "panel.db")); e != nil {
				t.Fatal("pre-operation backup missing", e)
			}
		})
	}
}
func TestUpdateBeforeStopValidationAndAutomaticRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executables use POSIX scripts; exercised on Linux CI")
	}
	for _, badBundle := range []bool{true, false} {
		t.Run(map[bool]string{true: "download-rejected", false: "activation-rollback"}[badBundle], func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "panel.db")
			database(t, db, "current-password-2026")
			binary := filepath.Join(dir, "soar-panel")
			ops := filepath.Join(dir, "soar-panel-ops")
			old := []byte("#!/bin/sh\nprintf '0.2.0-beta7\\n'\n")
			_ = os.WriteFile(binary, old, 0755)
			_ = os.WriteFile(ops, old, 0755)
			stops, starts := 0, 0
			s := &Service{DB: db, Binary: binary, OpsBinary: ops, StateDir: filepath.Join(dir, "jobs"), UID: -1, GID: -1, Control: func(_ context.Context, action string) error {
				if action == "stop" {
					stops++
				}
				if action == "start" {
					starts++
					if starts == 1 {
						return errors.New("new binary failed")
					}
				}
				return nil
			}, Health: func(context.Context) error { return nil }, Fetch: func(_ context.Context, v, work string) error {
				if badBundle {
					return errors.New("checksum mismatch")
				}
				for _, name := range []string{"soar-panel", "soar-panel-ops"} {
					if e := os.WriteFile(filepath.Join(work, name), []byte("#!/bin/sh\nprintf '"+v+"\\n'\n"), 0755); e != nil {
						return e
					}
				}
				return nil
			}}
			if e := s.Initialize(); e != nil {
				t.Fatal(e)
			}
			s.perform(panel.MaintenanceRequest{Action: "update", Version: "0.2.0-beta8"}, panel.MaintenanceJob{ID: "update-fixture", Kind: "update", State: "running"})
			job := s.Status()
			if badBundle {
				if stops != 0 || job.State != "failed" {
					t.Fatal("invalid download interrupted service", job)
				}
			} else {
				if job.State != "rolled_back" || stops < 2 {
					t.Fatal("activation did not roll back", job)
				}
			}
			restored, _ := os.ReadFile(binary)
			if !bytes.Equal(restored, old) {
				t.Fatal("old binary was not retained")
			}
		})
	}
}
func TestExtractorDoesNotInstallTraversalSymlinkOrDuplicate(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, variant := range []string{"safe", "missing", "symlink", "duplicate", "wrong_arch"} {
			t.Run(arch+"/"+variant, func(t *testing.T) {
				dir := t.TempDir()
				archive := filepath.Join(dir, "test.tar.gz")
				f, _ := os.Create(archive)
				z := gzip.NewWriter(f)
				tarw := tar.NewWriter(z)
				_ = tarw.WriteHeader(&tar.Header{Name: "../../escaped", Typeflag: tar.TypeReg, Mode: 0755, Size: 4})
				_, _ = io.WriteString(tarw, "evil")
				for _, name := range []string{"soar-panel", "soar-panel-ops", "soar-panel-security"} {
					if variant == "missing" && name == "soar-panel-ops" {
						continue
					}
					count := 1
					if variant == "duplicate" {
						count = 2
					}
					for i := 0; i < count; i++ {
						bundleArch := arch
						if variant == "wrong_arch" {
							if arch == "amd64" {
								bundleArch = "arm64"
							} else {
								bundleArch = "amd64"
							}
						}
						root := "soar-panel-0.2.0-beta8-linux-" + bundleArch
						header := &tar.Header{Name: root + "/bin/linux-" + bundleArch + "/" + name, Typeflag: tar.TypeReg, Size: 4, Mode: 0755}
						if name == "soar-panel-security" {
							header.Name = root + "/panel-security.sh"
						}
						if variant == "symlink" {
							header.Typeflag = tar.TypeSymlink
							header.Linkname = "/etc/passwd"
							header.Size = 0
						}
						_ = tarw.WriteHeader(header)
						if header.Typeflag == tar.TypeReg {
							_, _ = io.WriteString(tarw, "safe")
						}
					}
				}
				tarw.Close()
				z.Close()
				f.Close()
				target := filepath.Join(dir, "out")
				_ = os.Mkdir(target, 0700)
				e := extractBinariesArch(archive, target, "0.2.0-beta8", arch)
				if (e == nil) != (variant == "safe") {
					t.Fatal(variant, e)
				}
				if variant == "safe" {
					native := filepath.Join(dir, "native")
					if e := os.Mkdir(native, 0700); e != nil {
						t.Fatal(e)
					}
					if e := ExtractBinaries(archive, native, "0.2.0-beta8"); (e == nil) != (arch == runtime.GOARCH) {
						t.Fatal("extractor did not select the running architecture", e)
					}
				}
				files, _ := os.ReadDir(target)
				for _, file := range files {
					if !strings.HasPrefix(file.Name(), "soar-panel") {
						t.Fatal("unexpected extracted path")
					}
				}
				if _, e = os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(e) {
					t.Fatal("traversal entry was extracted")
				}
			})
		}
	}
}

func TestPanelRestartUsesAllowlistedControlAndPersistentHealthResult(t *testing.T) {
	for _, fail := range []string{"", "control", "health"} {
		t.Run(fail, func(t *testing.T) {
			calls := []string{}
			s := &Service{StateDir: t.TempDir(), Control: func(_ context.Context, action string) error {
				calls = append(calls, action)
				if fail == "control" {
					return errors.New("failed restart")
				}
				return nil
			}, Health: func(context.Context) error {
				if fail == "health" {
					return errors.New("unhealthy")
				}
				return nil
			}}
			if e := s.Initialize(); e != nil {
				t.Fatal(e)
			}
			s.perform(panel.MaintenanceRequest{Action: "restart"}, panel.MaintenanceJob{ID: "restart", Kind: "restart", State: "running"})
			want := "succeeded"
			if fail != "" {
				want = "failed"
			}
			if s.Status().State != want {
				t.Fatal(s.Status())
			}
			if len(calls) != 1 || calls[0] != "restart" {
				t.Fatal(calls)
			}
			loaded := &Service{StateDir: s.StateDir}
			if e := loaded.Initialize(); e != nil || loaded.Status().State != want {
				t.Fatal("restart result not persisted", e)
			}
			if _, e := s.Begin(panel.MaintenanceRequest{Action: "restart", Version: "1.0.0"}); e == nil {
				t.Fatal("restart accepted update fields")
			}
			s.Save(panel.MaintenanceJob{State: "running"})
			if _, e := s.Begin(panel.MaintenanceRequest{Action: "restart"}); e == nil {
				t.Fatal("overlapping restart accepted")
			}
		})
	}
}
