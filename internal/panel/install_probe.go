package panel

import (
	"errors"
	"time"
)

const installProbeName = "[installer connectivity test]"
const installProbeNote = "Reserved temporary installer account; expires automatically and is removed after checks."

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
		Name: installProbeName, Note: installProbeNote, Enabled: true,
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
