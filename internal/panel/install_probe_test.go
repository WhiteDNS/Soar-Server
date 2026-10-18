package panel

import (
	"errors"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestInstallationProbeExpiresCleansUpAndPreservesAccounts(t *testing.T) {
	s := testStore(t)
	user := account(t, s, 123456)
	probe, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	if probe.Status(time.Now()) != "active" || probe.Status(time.Now().Add(11*time.Minute)) != "expired" || probe.QuotaBytes != 1<<20 || probe.StreamLimit != 4 {
		t.Fatal("unbounded or inactive installer account", probe)
	}
	if _, err = s.Seed(probe.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(probe.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale probe retained", err)
	}
	if err = s.RemoveInstallProbe(user.ID); err == nil {
		t.Fatal("regular account removed by probe cleanup")
	}
	if err = s.RemoveInstallProbe(next.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveInstallProbe(next.ID); err != nil {
		t.Fatal("cleanup is not idempotent", err)
	}
	if _, err = s.Seed(next.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("probe key retained", err)
	}
	actual, err := s.Get(user.ID)
	if err != nil || actual != user {
		t.Fatal("existing account changed", err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatal("probe cleanup incomplete", err)
	}
}

func TestProbeCleanupCannotSelectAnAccountByItsEditableName(t *testing.T) {
	s := testStore(t)
	regular, err := s.Create(ClientInput{Name: installProbeName, Note: installProbeNote, Enabled: true, QuotaBytes: 123456, StreamLimit: 4, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveInstallProbe(regular.ID); err == nil {
		t.Fatal("editable name/note granted probe status")
	}
	if err := s.RemoveInstallProbe(probe.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(regular.ID)
	if err != nil || got != regular {
		t.Fatal("ordinary account changed during probe cleanup", err)
	}
}

func TestInstallationProbeHasAReservedSlotAtAccountCapacity(t *testing.T) {
	s := testStore(t)
	cfg, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	// Populate a full, valid database in one transaction; 512 individual fsyncs
	// obscure this capacity/cleanup regression on slower filesystems.
	err = s.db.Update(func(tx *bolt.Tx) error {
		for i := 0; i < MaxClients; i++ {
			id, zone, public, seed, err := randomIdentity(cfg.BaseZone)
			if err != nil {
				return err
			}
			c := Client{ID: id, Zone: zone, PublicKey: public, Name: "regular", Enabled: true, StreamLimit: 4, Revision: 1, UsageEpoch: 1}
			if err := putJSON(tx.Bucket(clients), []byte(id), c); err != nil {
				return err
			}
			if err := tx.Bucket(seeds).Put([]byte(id), seed); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	in := ClientInput{Name: "regular", Enabled: true, StreamLimit: 4}
	if _, err := s.Create(in); err == nil {
		t.Fatal("regular account exceeded capacity")
	}
	probe, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal("full server cannot run installation checks", err)
	}
	if _, err = s.Create(in); err == nil {
		t.Fatal("probe increased regular capacity")
	}
	if err = s.RemoveInstallProbe(probe.ID); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != MaxClients {
		t.Fatal("existing accounts not preserved", err)
	}
}

func TestExpiredInstallationProbeIsRemovedAfterInterruptedInstall(t *testing.T) {
	s := testStore(t)
	c, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Edit(c.ID, ClientInput{Name: c.Name, Note: c.Note, Enabled: true, QuotaBytes: c.QuotaBytes, StreamLimit: c.StreamLimit, ExpiresAt: time.Now().Add(-time.Second).UnixMilli(), Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_ = testManager(t, s, nil)
	if _, err := s.Get(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired installer account survived reconciliation", err)
	}
	if _, err := s.Seed(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired installer key survived reconciliation", err)
	}
}
