// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane (no build tools, no fixtures).
package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardedDrivers are the self-host driver entry points whose `main` must route
// through util.rc_underflow_guard.
//
// The rc over-release detector (__rc_underflow_count, bumped by every
// __fern_rc_dec that finds rc <= 0) shipped on all three backends and then
// nothing ran it, which is what let #6021 stay latent on main: the compiler
// over-released one of its OWN heap blocks on a shape as ordinary as a
// boolean struct field used as an `if` condition, kept emitting correct asm
// (the doubly-freed block was recycled harmlessly), and only crashed once an
// unrelated change shifted allocation sizes enough for the poisoned freelist
// head to be popped — ~50% of runs, presenting as "adding a field to
// LowerState breaks the compiler". The per-module fixpoint, all 335 fixtures
// and the native suite were green the whole time.
//
// The guard is what makes every existing driver-based test a detector: a
// corrupted freelist exits RC_UNDERFLOW_EXIT with a diagnostic instead of
// producing plausible output. It is one BSS load on the healthy path and
// changes no output, which is exactly what makes it easy to delete by
// accident — hence this check rather than trust.
var guardedDrivers = []string{
	"asm_ir_run.fern",
	"wasm_ir_run.fern",
}

func TestSelfHostDriversRunTheRcUnderflowGuard(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	for _, name := range guardedDrivers {
		path := filepath.Join(root, "examples", "self_host", name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(src), "util.rc_underflow_guard(") {
			t.Errorf("%s: `main` must return util.rc_underflow_guard(<driver>, run()) — "+
				"without it a driver that over-releases its own heap reports success (#6021)", name)
		}
	}

	// The helper itself must call the NATIVE spelling. The self-host lowering
	// also accepts the older `__rc_underflow` alias, but these drivers are
	// compiled by the native backend, whose checker registers only
	// `__rc_underflow_count` — the wrong spelling would not compile, and a
	// guard that reads nothing would compile and always pass.
	utilSrc, err := os.ReadFile(filepath.Join(root, "examples", "self_host", "util.fern"))
	if err != nil {
		t.Fatalf("read util.fern: %v", err)
	}
	if !strings.Contains(string(utilSrc), "__rc_underflow_count()") {
		t.Error("util.rc_underflow_guard must read __rc_underflow_count() — the detector it reports is the point")
	}
}

// repoRoot walks up from the working directory to the module root (the
// directory holding go.mod).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
