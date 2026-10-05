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
