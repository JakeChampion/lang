package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFern(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// `-check -target arm64-freestanding` is the #6509 deliverable: the target is
// declared and checkable before any backend emits for it, so E066 names
// the missing capability instead of a build dying on an undefined label.
func TestCheckTargetFreestandingRejectsHostBuiltins(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  print(\"hi\");\n  return 0;\n}\n")
	err := runCheck(entry, "arm64-freestanding")
	if err == nil {
		t.Fatal("print should be E066 under freestanding")
	}
	got := err.Error()
	for _, want := range []string{"E066", "arm64-freestanding", "`log`", "print"} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q:\n%s", want, got)
		}
	}
}

// The complement: a program that only computes checks clean, so the
// target is usable and not merely restrictive.
func TestCheckTargetFreestandingAllowsCore(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  let b: i64 = f64_bits(1.5);\n  return ((b + 1) as i32);\n}\n")
	if err := runCheck(entry, "arm64-freestanding"); err != nil {
		t.Fatalf("core-only program rejected: %v", err)
	}
}

// An unrequested check must not gain a capability gate. `-target`
// defaults to arm64, so passing it through unconditionally would start
// enforcing that set against every `fern -check` — this pins the
// empty-target opt-out that prevents it.
func TestCheckWithoutTargetSkipsEnforcement(t *testing.T) {
	// `subprocess` is interp-only: NO compiled target grants it, so a
	// check that enforced any target at all would reject this.
	entry := writeFern(t, "function main(): i32 {\n  let r = subprocess(\"/bin/echo\", [\"hi\"], \"\");\n  return r.exit_code;\n}\n")
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("bare -check should not enforce a target: %v", err)
	}
	if err := runCheck(entry, "arm64-linux"); err == nil {
		t.Fatal("-check -target arm64-linux should reject subprocess")
	}
}

// Compiling a declared-but-unemitted target is a clear refusal naming
// the check path, not the "unknown target" error (which would be false:
// the descriptor exists) and not a crash.
func TestCompileFreestandingRefusesClearly(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	out := filepath.Join(t.TempDir(), "out")
	code, err := run(entry, out, "arm64-freestanding", "", "", "", false, false, "", false, false, false, nil, false, "", false, nil)
	if code == 0 || err == nil {
		t.Fatalf("expected a refusal, got code=%d err=%v", code, err)
	}
	got := err.Error()
	for _, want := range []string{"no backend", "fern -check -target arm64-freestanding"} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unknown target") {
		t.Errorf("declared target must not report as unknown:\n%s", got)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("refused build should not have written an artifact")
	}
}

// An emitter that doesn't exist is refused by name rather than falling
// through to the default one, which would produce a working binary that is
// not the one asked for.
func TestBackendRejectsBadCombinations(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	cases := map[string]struct{ target, backend, want string }{
		"unknown backend": {"wasm32-wasi", "nope", `unknown -backend "nope"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			_, err := run(entry, out, c.target, c.backend, "", "", false, false, "", false, false, false, nil, false, "", false, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// `-emit core-module` replaces `-target wasm-bin`: an output format is a
// property of the artifact, not of the machine it runs on, so it does not
// belong in the target name (#6536). The bytes are unchanged — a core
// module starts 0x01 0x00 0x00 0x00 after the magic, where a component
// carries the component layer/version.
func TestEmitCoreModule(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	out := filepath.Join(t.TempDir(), "m.wasm")
	code, err := run(entry, out, "wasm32-wasi", "", "core-module", "", false, false, "", false, false, false, nil, false, "", false, nil)
	if err != nil || code != 0 {
		t.Fatalf("emit core-module: code=%d err=%v", code, err)
	}
	b, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(b) < 8 || string(b[:4]) != "\x00asm" {
		t.Fatalf("not a wasm artifact: % x", b[:min(8, len(b))])
	}
	if b[6] == 0x01 {
		t.Errorf("got a component, want a raw core module: % x", b[:8])
	}
}

// An output form the target has no notion of is refused by name, and the
// retired spelling is no longer a target.
func TestEmitRejectsBadCombinations(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	cases := map[string]struct{ target, emit, want string }{
		"no core-module for arm64": {"arm64-linux", "core-module", "not available for -target arm64"},
		"unknown emit":             {"wasm32-wasi", "nope", `unknown -emit "nope"`},
		"wasm-bin is not a target": {"wasm-bin", "", `unknown target "wasm-bin"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			_, err := run(entry, out, c.target, "", c.emit, "", false, false, "", false, false, false, nil, false, "", false, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// `-check -target` names the target the program is checked against, so one
// no descriptor answers for is refused rather than checked against nothing,
// for a file and for a workspace alike.
func TestCheckRefusesUnknownTarget(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	ws := t.TempDir()
	for name, src := range map[string]string{
		"fern.toml":   "[workspace]\nmembers = [\"a\"]\n",
		"a/fern.toml": "[package]\nname = \"a\"\n",
		"a/main.fern": "function main(): i32 {\n  return 0;\n}\n",
	} {
		p := filepath.Join(ws, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, arg := range []string{entry, ws} {
		err := runCheckTarget(arg, "bogus")
		if err == nil || !strings.Contains(err.Error(), `unknown target "bogus"`) {
			t.Errorf("-check -target bogus %s: %v, want the unknown-target refusal", arg, err)
		}
	}
}
