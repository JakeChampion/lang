package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// -sanitize on the self-host CLI (#7976's driver half): the flag turns the
// heap detectors on as FERN_SANITIZE=1 does, reaches every backend through
// the emit state rather than the environment, and names what a target's
// build carries, in native's words.
func TestSelfHostSanitizeFlag(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "dfree.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostDoubleFreeSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// buildEnv compiles with `env` added to a compiler environment that
	// otherwise carries no FERN_*; build passes none, so the flag decides.
	buildEnv := func(t *testing.T, name string, env []string, flags ...string) (string, string) {
		t.Helper()
		out := filepath.Join(dir, name)
		args := append(flags, "-o", out, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = append(e2eharness.ChildEnv(), env...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("fern %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
		return out, stderr.String()
	}
	build := func(t *testing.T, name string, flags ...string) (string, string) {
		t.Helper()
		return buildEnv(t, name, nil, flags...)
	}
	read := func(t *testing.T, path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("x86_64_reports", func(t *testing.T) {
		bin, warn := build(t, "dfree_san", "-sanitize", "-target", "x86-64-linux")
		if warn != "" {
			t.Errorf("a native target's -sanitize build warned:\n%s", warn)
		}
		stderr, code := hevRun(t, cli.runner, bin)
		if code != e2eharness.ExitSanitizer {
			t.Errorf("exit=%d, want %d", code, e2eharness.ExitSanitizer)
		}
		if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
			t.Errorf("stderr does not carry the diagnostic:\n%s", stderr)
		}
	})

	t.Run("off_is_silent", func(t *testing.T) {
		bin, _ := build(t, "dfree_off", "-target", "x86-64-linux")
		stderr, code := hevRun(t, cli.runner, bin)
		if code != 0 || stderr != "" {
			t.Errorf("exit=%d stderr=%q; an unsanitized build neither aborts nor reports", code, stderr)
		}
	})

	// wasm carries the census alone, and the flag says so.
	t.Run("wasm_census_and_note", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		wasm, warn := build(t, "dfree.wasm", "-sanitize", "-target", "wasm32-wasi", "-emit", "core-module")
		if !strings.Contains(warn, "fern: warning: -sanitize on -target wasm32-wasi carries the leak census only, not the rc over-release or use-after-free detectors") {
			t.Errorf("the coverage note is missing:\n%s", warn)
		}
		run := exec.Command("wasmtime", "run", wasm)
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if !strings.Contains(stderr.String(), "leakcheck: allocs=") {
			t.Errorf("the census did not report; the flag did not reach the wasm emitter:\n%s", stderr.String())
		}
	})

	// The flag and FERN_SANITIZE=1 are two ways into one mode: the emitted
	// code is the same bytes on every target. The flag's spellings follow
	// Go's flag.Bool, as native's do: =true is the flag and =false is its
	// absence.
	t.Run("flag_matches_env", func(t *testing.T) {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			flag, _ := build(t, "flag-"+target+".s", "-sanitize", "-target", target, "-emit", "asm")
			env, _ := buildEnv(t, "env-"+target+".s", []string{"FERN_SANITIZE=1"}, "-target", target, "-emit", "asm")
			if read(t, flag) != read(t, env) {
				t.Errorf("%s: -sanitize and FERN_SANITIZE=1 emit different code", target)
			}
		}
		on, _ := build(t, "on.s", "-sanitize", "-target", "x86-64-linux", "-emit", "asm")
		tru, _ := build(t, "true.s", "-sanitize=true", "-target", "x86-64-linux", "-emit", "asm")
		off, _ := build(t, "off.s", "-target", "x86-64-linux", "-emit", "asm")
		fls, _ := build(t, "false.s", "-sanitize=false", "-target", "x86-64-linux", "-emit", "asm")
		if read(t, tru) != read(t, on) {
			t.Error("-sanitize=true emits other code than -sanitize")
		}
		if read(t, fls) != read(t, off) {
			t.Error("-sanitize=false emits other code than a build without the flag")
		}
		if read(t, on) == read(t, off) {
			t.Error("-sanitize changed nothing in the emitted code")
		}
	})
}
