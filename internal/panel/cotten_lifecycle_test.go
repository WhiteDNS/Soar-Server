package panel

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestCottenRevocationAndKeyLifecycle(t *testing.T) {
	for _, action := range []string{"disable", "expire", "rotate", "delete", "soar only"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "cotten", Engines: "cotten", Enabled: true, StreamLimit: 4})
			if err != nil {
				t.Fatal(err)
			}
			var peer net.Conn
			m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
				local, remote := net.Pipe()
				peer = remote
				return local, nil
			})
			m.mu.RLock()
			rt := m.accounts[c.ID]
			m.mu.RUnlock()
			engine, err := m.cottenFor(rt)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := m.dial(rt, context.Background(), "tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			defer peer.Close()
			in := ClientInput{Name: c.Name, Engines: c.Engines, Enabled: true, StreamLimit: c.StreamLimit, Revision: c.Revision}
			switch action {
			case "disable":
				in.Enabled = false
				_, err = s.Edit(c.ID, in)
			case "expire":
				in.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				_, err = s.Edit(c.ID, in)
			case "rotate":
				_, err = s.Rotate(c.ID)
			case "delete":
				err = s.DeleteMany([]string{c.ID})
			case "soar only":
				in.Engines = "soar"
				_, err = s.Edit(c.ID, in)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Reconcile(); err != nil {
				t.Fatal(err)
			}
			peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err = peer.Read(make([]byte, 1)); err != io.EOF {
				t.Fatal("revoked Cotten egress was not closed", err)
			}
			if rt.ctx.Err() == nil || len(engine.HandleDNS([]byte("invalid"))) != 0 {
				t.Fatal("old runtime remained available")
			}
			if m.ActiveCounts()[c.ID] != 0 {
				t.Fatal("revocation retained connections")
			}
		})
	}
}

func TestCottenKeysSurviveFullBackup(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "backup both", Engines: "both", Enabled: true, StreamLimit: 8})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Profiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/restore.db"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Backup(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	after, err := restored.Profiles(c.ID)
	if err != nil || before["cotten"] != after["cotten"] || before["soar"] != after["soar"] {
		t.Fatal("backup changed engine keys or profiles", err)
	}
}
