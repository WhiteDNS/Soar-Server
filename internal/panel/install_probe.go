package panel

import (
	"errors"
	"time"
)

const installProbeName = "[installer connectivity test]"
const installProbeNote = "Reserved temporary installer account; expires automatically and is removed after checks."

// WriteInstallCottenProbe exports only the short-lived install account, to a
// private file. Ordinary account secrets are never printed by this command.
func (s *Store) WriteInstallCottenProbe(id, path string) error {
	c, err := s.Get(id)
	if err != nil {
		return err
	}
	if !isInstallProbe(c) {
		return errors.New("not an installation account")
	}
	seed, err := s.Seed(id)
	if err != nil {
		return err
	}
	return WritePrivateJSON(path, map[string]string{"domain": c.cottenZone(), "key": cottenKey(seed)})
}

// WriteInstallMasterDNSProbe exports only the bounded temporary test account.
func (s *Store) WriteInstallMasterDNSProbe(id, path string) error {
	c, err := s.Get(id)
	if err != nil {
		return err
	}
	if !isInstallProbe(c) {
		return errors.New("not an installation account")
	}
	seed, err := s.Seed(id)
	if err != nil {
		return err
	}
	return WritePrivateJSON(path, map[string]string{"domain": c.masterDNSZone(), "key": masterDNSKey(seed)})
}

// CreateInstallProbe is an offline installer operation, never exposed by HTTP.
// One reserved slot allows checking a full installation without using a user's
// credentials or quota. Expiry and a small quota bound an interrupted install.
func (s *Store) CreateInstallProbe() (Client, error) {
	list, err := s.List()
	if err != nil {
		return Client{}, err
	}
	for _, c := range list {
		if isInstallProbe(c) {
			if err := s.Delete(c.ID); err != nil {
				return Client{}, err
			}
		}
	}
	return s.create(ClientInput{
		Name: installProbeName, Note: installProbeNote, Enabled: true, Engines: "all",
		QuotaBytes: 1 << 20, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(), StreamLimit: 4,
	}, true)
}

func isInstallProbe(c Client) bool {
	return c.InstallerProbe
}

func (s *Store) RemoveInstallProbe(id string) error {
	c, err := s.Get(id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isInstallProbe(c) {
		return errors.New("refusing to remove a regular account as an installation probe")
	}
	return s.Delete(id)
}
