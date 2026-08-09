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

// `-check -target freestanding` is the #6509 deliverable: the target is
// declared and checkable before any backend emits for it, so E066 names
// the missing capability instead of a build dying on an undefined label.
func TestCheckTargetFreestandingRejectsHostBuiltins(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  print(\"hi\");\n  return 0;\n}\n")
	err := runCheck(entry, "freestanding")
	if err == nil {
		t.Fatal("print should be E066 under freestanding")
	}
	got := err.Error()
	for _, want := range []string{"E066", "freestanding", "`log`", "print"} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q:\n%s", want, got)
		}
	}
}

// The complement: a program that only computes checks clean, so the
// target is usable and not merely restrictive.
func TestCheckTargetFreestandingAllowsCore(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  var b: i64 = f64_bits(1.5);\n  return ((b + 1) as i32);\n}\n")
	if err := runCheck(entry, "freestanding"); err != nil {
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
	entry := writeFern(t, "function main(): i32 {\n  var r = subprocess(\"/bin/echo\", [\"hi\"], \"\");\n  return r.exit_code;\n}\n")
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("bare -check should not enforce a target: %v", err)
	}
	if err := runCheck(entry, "arm64"); err == nil {
		t.Fatal("-check -target arm64 should reject subprocess")
	}
}

// Compiling a declared-but-unemitted target is a clear refusal naming
// the check path, not the "unknown target" error (which would be false:
// the descriptor exists) and not a crash.
func TestCompileFreestandingRefusesClearly(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	out := filepath.Join(t.TempDir(), "out")
	code, err := run(entry, out, "freestanding", "", "", false, false, "", false, false, false, nil, false, "", false, nil)
	if code == 0 || err == nil {
		t.Fatalf("expected a refusal, got code=%d err=%v", code, err)
	}
	got := err.Error()
	for _, want := range []string{"no backend", "fern -check -target freestanding"} {
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

// The SSA backends used to be their own `-target` values with no
// descriptor, which meant selecting one silently opted the build out of
// capability enforcement: the failure came from the backend as an
// unknown-callee message with no source position, error code, or
// `fern explain`. As `-backend ssa` on an ordinary target they keep the
// target's descriptor, so E066 applies to them like anything else (#6536).
func TestBackendSSAKeepsCapabilityEnforcement(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  var r = subprocess(\"/bin/echo\", [\"hi\"], \"\");\n  return r.exit_code;\n}\n")
	for _, target := range []string{"wasm", "arm64"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			code, err := run(entry, out, target, "ssa", "", false, false, "", false, false, false, nil, false, "", false, nil)
			if code == 0 || err == nil {
				t.Fatalf("expected E066, got code=%d err=%v", code, err)
			}
			got := err.Error()
			for _, want := range []string{"E066", "subprocess"} {
				if !strings.Contains(got, want) {
					t.Errorf("error missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// An emitter that doesn't exist for the target is refused by name rather
// than falling through to the default one, which would produce a working
// binary that is not the one asked for.
func TestBackendRejectsBadCombinations(t *testing.T) {
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	cases := map[string]struct{ target, backend, want string }{
		"no ssa for x86-64": {"x86-64", "ssa", "not available for -target x86-64"},
		"unknown backend":   {"wasm", "nope", `unknown -backend "nope"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			_, err := run(entry, out, c.target, c.backend, "", false, false, "", false, false, false, nil, false, "", false, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
