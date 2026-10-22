package panel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUsageGroupCommitPreservesDurableTotalsAndPolicy(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	before := s.AccountingStats()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 8; j++ {
				if allowed, _, err := s.reserveEpoch(c.ID, 1024, true, c.UsageEpoch); err != nil || allowed != 1024 {
					t.Errorf("reservation %d: %v", allowed, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	after := s.AccountingStats()
	t.Logf("256 durable reservations used %d shared commits", after["commits"]-before["commits"])
	if after["requests"]-before["requests"] != 256 {
		t.Fatalf("lost operations: %v", after)
	}
	if after["commits"]-before["commits"] >= 128 {
		t.Fatalf("concurrent operations were not grouped: %v", after)
	}
	actual, err := s.Get(c.ID)
	if err != nil || actual.UploadBytes != 256*1024 {
		t.Fatalf("totals: %+v %v", actual, err)
	}
	var backup bytes.Buffer
	if err = s.Backup(&backup); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restore.db")
	if err = os.WriteFile(path, backup.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	durable, err := restored.Get(c.ID)
	if err != nil || durable != actual {
		t.Fatalf("committed data changed after reopening: %+v %v", durable, err)
	}
	in := input(actual)
	in.Enabled = false
	if _, err = s.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.reserveEpoch(c.ID, 1, true, c.UsageEpoch); !errors.Is(err, ErrDenied) {
		t.Fatalf("disabled account admitted: %v", err)
	}
}

func TestUsageBatchRejectsOneRequestWithoutLosingOtherAccounts(t *testing.T) {
	s := testStore(t)
	a, b := account(t, s, 3), account(t, s, 0)
	if _, _, err := s.reserveEpoch(a.ID, 3, true, a.UsageEpoch); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, _, e := s.reserveEpoch(a.ID, 1, true, a.UsageEpoch); !errors.Is(e, ErrDenied) {
				t.Errorf("exhausted accepted: %v", e)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if _, _, e := s.reserveEpoch(b.ID, 7, false, b.UsageEpoch); e != nil {
				t.Error(e)
			}
		}()
	}
	close(start)
	wg.Wait()
	actual, _ := s.Get(b.ID)
	if actual.DownloadBytes != 112 {
		t.Fatalf("unrelated account lost committed traffic: %d", actual.DownloadBytes)
	}
	if _, _, err := s.reserveDatagramEpoch(a.ID, 4, true, a.UsageEpoch); !errors.Is(err, ErrDenied) {
		t.Fatal("partial datagram admitted", err)
	}
	if err := s.ResetUsage(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.reserveEpoch(b.ID, 5, true, b.UsageEpoch); !errors.Is(err, ErrDenied) {
		t.Fatal("old epoch admitted", err)
	}
	if err := s.refund(b.ID, 112, false, b.UsageEpoch); err != nil {
		t.Fatal(err)
	}
	actual, _ = s.Get(b.ID)
	if actual.Used() != 0 {
		t.Fatal("reset or old refund changed usage")
	}
}

type pausedBackupWriter struct {
	started, release chan struct{}
	once             sync.Once
}

func (w *pausedBackupWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.release })
	return len(p), nil
}

func TestSlowBackupDownloadDoesNotBlockUsageAndRemovesSnapshot(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	w := &pausedBackupWriter{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.Backup(w) }()
	<-w.started
	released := false
	defer func() {
		if !released {
			close(w.release)
			<-done
		}
	}()
	writes := make(chan error, 1)
	go func() {
		for i := 0; i < 64; i++ {
			if _, _, err := s.reserveEpoch(c.ID, 1024, true, c.UsageEpoch); err != nil {
				writes <- err
				return
			}
		}
		writes <- nil
	}()
	select {
	case err := <-writes:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow backup stalled accounting")
	}
	close(w.release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(s.db.Path()), ".panel-backup-*.db"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("snapshot files retained: %v %v", matches, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.BackupContext(ctx, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("backup cancellation: %v", err)
	}
}

func TestUsageBatcherCloseDrainsAcceptedRequests(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	path := s.db.Path()
	if _, _, err := s.reserveEpoch(c.ID, 9, true, c.UsageEpoch); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.reserveEpoch(c.ID, 1, true, c.UsageEpoch); err == nil {
		t.Fatal("closed store accepted accounting")
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.Get(c.ID)
	if err != nil || actual.UploadBytes != 9 {
		t.Fatalf("shutdown lost usage: %+v %v", actual, err)
	}
}
