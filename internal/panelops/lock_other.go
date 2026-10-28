//go:build !linux

package panelops

import "errors"

func acquireMaintenanceLock(path string) (func(), error) {
	if path != "" {
		return nil, errors.New("maintenance locking requires Linux")
	}
	return func() {}, nil
}
