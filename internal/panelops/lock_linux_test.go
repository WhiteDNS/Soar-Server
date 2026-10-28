//go:build linux

package panelops

import (
	"path/filepath"
	"testing"

	"soar-server-setup/internal/panel"
)

func TestMaintenanceRejectsSharedTerminalLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maintenance.lock")
	unlock, err := acquireMaintenanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{LockFile: path}
	if _, err = s.Begin(panel.MaintenanceRequest{Action: "restart"}); err == nil {
		t.Fatal("maintenance started while terminal held the lock")
	}
	unlock()
	unlockAgain, err := acquireMaintenanceLock(path)
	if err != nil {
		t.Fatal("lock was not released", err)
	}
	unlockAgain()
}
