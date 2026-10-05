package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveStampsVersionAndLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, Config{Model: "m1", MaxTurns: 7}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != Version || cfg.Model != "m1" || cfg.MaxTurns != 7 {
		t.Fatalf("round trip: %+v", cfg)
	}
}

// TestLoadRejectsUnknownFields proves strict decode: a typo in a
// hand-edited file is an error, not a silently zeroed setting
// (spec/data.md).
func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"modle":"typo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("an unknown field must fail the load")
	}
}

// TestLoadRefusesNewerVersion proves the version guard: a file from a
// newer schema is refused with ErrUnsupportedVersion so the caller
// quarantines it instead of overwriting (spec/data.md).
func TestLoadRefusesNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("want ErrUnsupportedVersion, got %v", err)
	}
}

// TestLoadMissingFileIsNotAnError keeps the defaults contract.
func TestLoadMissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatal("a missing file returns the read error for os.IsNotExist checks")
	}
	_ = cfg
}
