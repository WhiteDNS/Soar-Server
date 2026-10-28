//go:build linux

package panelops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func acquireMaintenanceLock(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another installer or terminal maintenance operation is running")
	}
	return func() { _ = f.Close() }, nil
}
