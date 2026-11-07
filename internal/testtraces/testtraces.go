// Package testtraces locates recorded USB traces for tests. Recordings of
// real scans are not part of this repository (they contain scanned images
// and the manufacturer's firmware upload). Tests that need them run only
// when SCANSTATION_TRACES names a directory with the recordings
// (NAME.jsonl.gz); otherwise they are skipped. The synthetic scanner
// (sim/synth) covers the same code paths without recordings.
package testtraces

import (
	"os"
	"path/filepath"
	"testing"
)

// Env is the environment variable naming the recordings directory.
const Env = "SCANSTATION_TRACES"

// Path returns the path of recording name, or skips the test.
func Path(t testing.TB, name string) string {
	t.Helper()
	dir := os.Getenv(Env)
	if dir == "" {
		t.Skipf("needs the recording %s: set %s to a directory with private recordings", name, Env)
	}
	p := filepath.Join(dir, name+".jsonl.gz")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s is set but %v", Env, err)
	}
	return p
}
