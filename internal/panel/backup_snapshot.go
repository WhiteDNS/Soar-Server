package panel

import (
	"context"
	"io"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

type backupContextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w backupContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}

// A slow download must never hold a database transaction open: a reader pins
// pages and can block all writes when bbolt needs to remap a growing database.
func (s *Store) BackupContext(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.db.Path()), ".panel-backup-*.db")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	err = s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(backupContextWriter{ctx, file})
		return err
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(backupContextWriter{ctx, w}, file)
	return err
}
