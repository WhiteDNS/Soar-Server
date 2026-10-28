package main

import (
	"os"
	"path/filepath"

	managementassets "soar-server-setup"
)

func installManagementMenu(path, alias string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".soar-menu-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(managementassets.MenuScript); err == nil {
		err = file.Chmod(0755)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	if _, err = os.Lstat(alias); os.IsNotExist(err) {
		return os.Symlink(path, alias)
	}
	return err
}
