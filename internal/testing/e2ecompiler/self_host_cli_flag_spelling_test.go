package e2ecompiler

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostShippedFernFlagSpellings(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	env := []string{"FERN_CACHE_DIR=" + filepath.Join(t.TempDir(), "cache")}
	dir := t.TempDir()
	ok := writeShippedSrc(t, dir, "ok.fern", "function main(): i32 {\n  return 3;\n}\n")
	asserts := writeShippedSrc(t, dir, "asserts.fern", "function main(): i32 {\n  assert(1 == 2);\n  return 3;\n}\n")
	bad := writeShippedSrc(t, dir, "bad.fern", "function main(): i32 {\n  let x: i32 = \"s\";\n  return x;\n}\n")
	x86 := "x86-64-linux"

	for _, c := range []struct{ spelt, canonical []string }{
		{[]string{"--check", bad}, []string{"-check", bad}},
		{[]string{"-check=true", bad}, []string{"-check", bad}},
		{[]string{"-check=false", "-interp", ok}, []string{"-interp", ok}},
		{[]string{"--interp", ok}, []string{"-interp", ok}},
		{[]string{"-target=" + x86, ok}, []string{"-target", x86, ok}},
		{[]string{"--target", x86, ok}, []string{"-target", x86, ok}},
		{[]string{"--target=" + x86, "--emit=asm", ok}, []string{"-target", x86, "-emit", "asm", ok}},
		{[]string{"-cover=false", "-target", "wasm32-wasi", "-emit", "asm", ok}, []string{"-target", "wasm32-wasi", "-emit", "asm", ok}},
		{[]string{"-cover=true", "-target", "wasm32-wasi", "-emit", "asm", ok}, []string{"-cover", "-target", "wasm32-wasi", "-emit", "asm", ok}},
		{[]string{"--cover=1", "-target", "wasm32-wasi", "-emit", "asm", ok}, []string{"-cover", "-target", "wasm32-wasi", "-emit", "asm", ok}},
		{[]string{"-g=true", "-target", x86, ok}, []string{"-g", "-target", x86, ok}},
		{[]string{"-g=false", "-target", x86, ok}, []string{"-target", x86, ok}},
		{[]string{"-O=true", "-target", x86, asserts}, []string{"-O", "-target", x86, asserts}},
		{[]string{"-O=false", "-target", x86, asserts}, []string{"-target", x86, asserts}},
		{[]string{"--backtrace=0", "-target", x86, ok}, []string{"-backtrace=false", "-target", x86, ok}},
		{[]string{"-color=never", "-check", bad}, []string{"-color", "never", "-check", bad}},
		{[]string{"--color=always", "-check", bad}, []string{"-color", "always", "-check", bad}},
		{[]string{"-explain=E001"}, []string{"-explain", "E001"}},
		{[]string{"--explain", "E001"}, []string{"-explain", "E001"}},
		{[]string{"-lint", "-lint-set=cyclomatic-complexity=deny", ok}, []string{"-lint", "-lint-set", "cyclomatic-complexity=deny", ok}},
		{[]string{"-lint", "--lint-cover=none.txt", ok}, []string{"-lint", "-lint-cover", "none.txt", ok}},
		{[]string{"--version"}, []string{"-version"}},
		{[]string{"--help"}, []string{"-h"}},
		{[]string{"--h"}, []string{"-h"}},
		{[]string{"-h=false", "-interp", ok}, []string{"-interp", ok}},
	} {
		want := runCLI(t, fern, env, c.canonical...)
		got := runCLI(t, fern, env, c.spelt...)
		if got != want {
			t.Errorf("fern %s behaves unlike fern %s:\n got %s\nwant %s",
				strings.Join(c.spelt, " "), strings.Join(c.canonical, " "), got, want)
		}
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-target=", ok}, "fern: -target= has no value: write -target ISA-ENV or -target=ISA-ENV\n"},
		{[]string{"--o="}, "fern: -o= has no value: write -o OUT or -o=OUT\n"},
		{[]string{"-target"}, "fern: -target needs a value: write -target ISA-ENV or -target=ISA-ENV\n"},
		{[]string{"-cover=maybe", ok}, "fern: -cover is a boolean: write -cover=true or -cover=false (got \"maybe\")\n"},
		{[]string{"--check=", ok}, "fern: -check is a boolean: write -check=true or -check=false (got \"\")\n"},
		{[]string{"---check", ok}, "fern: unknown flag: ---check\n"},
		{[]string{"--bogus=1", ok}, "fern: unknown flag: --bogus=1\n"},
	} {
		stdout, stderr, code := runHelp(t, fern, env, c.args...)
		if code != 2 || stdout != "" || stderr != c.want {
			t.Errorf("fern %s: exit %d, stdout %q, stderr %q; want exit 2 and %q",
				strings.Join(c.args, " "), code, stdout, stderr, c.want)
		}
	}
}

type cliRun struct {
	stdout, stderr string
	code           int
}

func (r cliRun) String() string {
	return fmt.Sprintf("exit %d, %d bytes of stdout, stderr %q", r.code, len(r.stdout), r.stderr)
}

func runCLI(t *testing.T, bin string, env []string, args ...string) cliRun {
	t.Helper()
	stdout, stderr, code := runHelp(t, bin, env, args...)
	return cliRun{stdout, stderr, code}
}
