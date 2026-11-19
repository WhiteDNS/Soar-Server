package main

import (
	"errors"
	"io"
	"os"
)

// readPasswordFile reads an administrator password from a 0600 file, or from
// standard input for "-": a container user cannot read a host file that keeps
// its owner and mode, and passwords must never be command-line arguments.
func readPasswordFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("--password-file is required; passwords must not be passed on the command line")
	}
	if path == "-" {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 257))
		if err == nil && len(raw) > 256 {
			err = errors.New("invalid password input")
		}
		return raw, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 256 {
		return nil, errors.New("invalid password file")
	}
	if stat.Mode().Perm()&0077 != 0 && os.PathSeparator != '\\' {
		return nil, errors.New("password file must have mode 0600")
	}
	return os.ReadFile(path)
}
