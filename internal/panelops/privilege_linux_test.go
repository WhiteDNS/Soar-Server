//go:build linux

package panelops

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBackupChecksRunOnAPanelUserCopy(t *testing.T) {
	dir, err := os.MkdirTemp("", "soar-privilege-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private")
	if err = os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(private, "restore.db")
	if err = os.WriteFile(candidate, []byte("candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	// The child reports its own user and whether it can read the path it received.
	binary := filepath.Join(dir, "fake-panel")
	script := "#!/bin/sh\nif [ \"$1\" = preserve-restore-admin ]; then echo changed > \"$3\"; exit 0; fi\ntest -r \"$3\" && echo \"{\\\"base_zone\\\":\\\"$(id -u)\\\",\\\"sha256\\\":\\\"$3\\\"}\"\n"
	if err = os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: filepath.Join(dir, "panel.db"), Binary: binary, UID: -1, GID: -1}
	want := os.Getuid()
	if want == 0 {
		// Root must end up as this unprivileged user, never as root.
		s.UID, s.GID, want = 65534, 65534, 65534
		if err = os.Chown(dir, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.inspect(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if summary.BaseZone != strconv.Itoa(want) {
		t.Fatal("backup inspection ran as the wrong user", summary.BaseZone)
	}
	if summary.SHA256 == candidate || strings.HasPrefix(summary.SHA256, private) {
		t.Fatal("child received the private candidate", summary.SHA256)
	}
	if err = s.preserve(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(candidate); string(raw) != "changed\n" {
		t.Fatal("restore candidate did not receive the child's result", string(raw))
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".soar-maintenance-*")); len(left) != 0 {
		t.Fatal("temporary copy left behind", left)
	}
}
