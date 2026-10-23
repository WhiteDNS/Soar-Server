package panel

import "testing"

func TestStreamSettingsOmittedLimitPreservesNewerValue(t *testing.T) {
	s := testStore(t)
	stale, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	current := stale
	current.ServerStreamLimit = 37
	if err := s.SaveSettings(current); err != nil {
		t.Fatal(err)
	}
	// A previously loaded form from an older client omits the new field.
	// Preserve the latest committed value rather than the stale form's zero.
	if err := s.saveSettings(stale, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings()
	if err != nil || got.ServerStreamLimit != 37 {
		t.Fatalf("omitted field replaced the newer server limit: %d, %v", got.ServerStreamLimit, err)
	}
	// An explicit zero still disables the aggregate cap.
	zero := 0
	if err := s.saveSettings(stale, &zero); err != nil {
		t.Fatal(err)
	}
	got, err = s.Settings()
	if err != nil || got.ServerStreamLimit != 0 {
		t.Fatalf("explicit unlimited limit was not applied: %d, %v", got.ServerStreamLimit, err)
	}
}
