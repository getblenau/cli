package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedUpdateCache points the config dir at a temp dir and writes a FRESH
// update-check cache, so nothing here touches the network. Both env vars are
// set because ConfigPath reads APPDATA on Windows and XDG_CONFIG_HOME elsewhere.
func seedUpdateCache(t *testing.T, latest string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("BLENAU_NO_UPDATE_CHECK", "")

	p, err := updateCachePath()
	if err != nil {
		t.Fatalf("updateCachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.Marshal(updateCache{CheckedAt: time.Now().Unix(), LatestVersion: latest})
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

func TestOutdatedBinaryHintOnlyForUnknownCommandAndOnlyWhenBehind(t *testing.T) {
	seedUpdateCache(t, "v0.20.0")

	unknown := errors.New(`unknown command "status" for "blenau"`)

	// The case this exists for: an old binary reporting a real command as
	// nonexistent. The hint must name BOTH versions, or the reader cannot tell
	// how far behind they are.
	hint := OutdatedBinaryHint("0.14.0", unknown)
	if hint == "" {
		t.Fatal("outdated binary + unknown command should produce a hint")
	}
	if !strings.Contains(hint, "0.14.0") || !strings.Contains(hint, "v0.20.0") {
		t.Errorf("hint must name current and latest version, got: %q", hint)
	}
	if !strings.Contains(hint, "blenau update") {
		t.Errorf("hint must name the remedy, got: %q", hint)
	}

	// Up to date: an unknown command really is unknown. Saying otherwise would
	// send the reader chasing a version that would not help.
	if got := OutdatedBinaryHint("0.20.0", unknown); got != "" {
		t.Errorf("up-to-date binary must not blame the version, got: %q", got)
	}

	// Behind, but the failure has its own explanation. Attaching a version
	// notice to every error is how a reader learns to ignore it.
	for _, other := range []error{
		errors.New("401 unauthorized: token rejected"),
		errors.New("document not found: docs/missing.md"),
		errors.New(`required flag "path" not set`),
	} {
		if got := OutdatedBinaryHint("0.14.0", other); got != "" {
			t.Errorf("unrelated error must not get the hint: %v -> %q", other, got)
		}
	}

	if got := OutdatedBinaryHint("0.14.0", nil); got != "" {
		t.Errorf("nil error must not get the hint, got: %q", got)
	}
}

func TestOutdatedBinaryHintCoversUnknownFlag(t *testing.T) {
	seedUpdateCache(t, "v0.20.0")
	// A flag added in a later release fails exactly like a missing command, and
	// leads an agent to the same wrong conclusion.
	for _, e := range []error{
		errors.New("unknown flag: --server"),
		errors.New("unknown shorthand flag: 's' in -s"),
	} {
		if OutdatedBinaryHint("0.14.0", e) == "" {
			t.Errorf("expected hint for %v", e)
		}
	}
}

func TestUpdateCheckOptOutSilencesEveryChannel(t *testing.T) {
	seedUpdateCache(t, "v0.20.0")
	t.Setenv("BLENAU_NO_UPDATE_CHECK", "1")

	if latest, stale := UpdateAvailable("0.14.0"); latest != "" || stale {
		t.Errorf("opt-out must report nothing, got (%q,%v)", latest, stale)
	}
	if got := OutdatedBinaryHint("0.14.0", errors.New(`unknown command "status"`)); got != "" {
		t.Errorf("opt-out must silence the hint, got: %q", got)
	}
}

func TestUpdateAvailableReportsBehindAndCurrent(t *testing.T) {
	seedUpdateCache(t, "v0.20.0")

	latest, stale := UpdateAvailable("0.14.0")
	if latest != "v0.20.0" || !stale {
		t.Errorf("behind: got (%q,%v) want (v0.20.0,true)", latest, stale)
	}

	// Still reports the tag when current — the manifest distinguishes "checked,
	// up to date" from "could not check", and only an empty tag means the latter.
	latest, stale = UpdateAvailable("0.20.0")
	if latest != "v0.20.0" || stale {
		t.Errorf("current: got (%q,%v) want (v0.20.0,false)", latest, stale)
	}
}
