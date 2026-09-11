package coreutils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// #8891: the self-host build of uniq segfaulted on every long option once
// `Getopt` had as many fields as uniq's `Cfg`. The self-host's tail release
// at `return long_option(s, a)` in `Getopt.next` freed `s`'s box, though
// long_option hands that box back INSIDE the tuple it returns (through its
// alias `var s: Getopt = g`); the freed block sat in the same freelist class
// as `Cfg` from then on, and the next `Cfg { ...cfg, … }` in uniq's option
// loop recycled it under the live Getopt. EQUAL FIELD COUNTS are what put
// the two structs in one class, which is why every build where they
// differed was fine.
//
// The recipe is the issue's own: uniq compiled by the self-host against a
// `Getopt` padded to uniq's `Cfg` field count, then a long option that
// matches. Both counts are read from the sources rather than written down,
// so the gate keeps synthesising the collision as either struct grows — and
// the tree's own gnu.fern is copied, not edited, because the field count is
// the trigger and not the fix.
func TestSelfHostUniqLongOptionWithSameFieldCountStructs(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	libs, err := filepath.Glob(filepath.Join(root, "coreutils", "lib", "*.fern"))
	if err != nil || len(libs) == 0 {
		t.Fatalf("coreutils/lib: %v", err)
	}
	for _, lib := range libs {
		b, err := os.ReadFile(lib)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "lib", filepath.Base(lib)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := os.ReadFile(filepath.Join(root, "coreutils", "uniq.fern"))
	if err != nil {
		t.Fatal(err)
	}
	gnuPath := filepath.Join(work, "lib", "gnu.fern")
	gnu, err := os.ReadFile(gnuPath)
	if err != nil {
		t.Fatal(err)
	}
	want := structFields(t, string(src), "struct Cfg {", "uniq.fern")
	have := structFields(t, string(gnu), "pub struct Getopt {", "gnu.fern")
	if pad := want - have; pad > 0 {
		if err := os.WriteFile(gnuPath, []byte(padGetopt(t, string(gnu), pad)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	uniqSrc := filepath.Join(work, "uniq.fern")
	if err := os.WriteFile(uniqSrc, src, 0o644); err != nil {
		t.Fatal(err)
	}

	native := filepath.Join(work, "uniq-native")
	if out, err := exec.Command(e2eharness.BuildLangBinForInterp(t), "-target", fernTarget(t), "-o", native, uniqSrc).CombinedOutput(); err != nil {
		t.Fatalf("native compile of the patched uniq: %v\n%s", err, out)
	}
	ours := filepath.Join(work, "uniq-selfhost")
	argv := crossArgv(selfHostCompiler(t), "-target", fernTarget(t), uniqSrc,
		filepath.Join(root, "internal", "stdlib"), "-o", ours)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host compile of the patched uniq: %v\n%s", err, out)
	}

	input := filepath.Join(work, "d1")
	if err := os.WriteFile(input, []byte("a\na\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--count", input}, {"--repeated", input}, {"--unique", input}, {"-c", input}} {
		want := runPatchedUniq(t, native, args)
		got := runPatchedUniq(t, ours, args)
		if want.exit != got.exit || !bytes.Equal(want.stdout, got.stdout) || !bytes.Equal(want.stderr, got.stderr) {
			t.Errorf("uniq %s: native (%s) %q / %q, self-host (%s) %q / %q",
				strings.Join(args, " "), want.how(), want.stdout, want.stderr, got.how(), got.stdout, got.stderr)
		}
	}
}

// structFields counts the fields of the one-line struct declaration that
// starts with `head`. Every struct in this tree is written on one line and
// no field type here carries a comma, so the count is the commas plus one.
func structFields(t *testing.T, src, head, where string) int {
	t.Helper()
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("%s no longer declares `%s`; this gate patches that declaration", where, head)
	}
	body := src[i+len(head):]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatalf("%s: `%s` is not the one-line declaration this gate reads", where, head)
	}
	return strings.Count(body[:end], ",") + 1
}

// padGetopt adds `pad` fields to Getopt's declaration and to the literal
// `getopt_new` builds, which is the only one that names every field.
func padGetopt(t *testing.T, gnu string, pad int) string {
	t.Helper()
	var decl, init strings.Builder
	for i := 0; i < pad; i++ {
		fmt.Fprintf(&decl, ", pad%d: i32", i)
		fmt.Fprintf(&init, ", pad%d: 0", i)
	}
	out := insertBefore(t, gnu, "pub struct Getopt {", " }", decl.String())
	return insertBefore(t, out, "return Getopt { argv: argv,", " };", init.String())
}

// insertBefore splices `text` in just before the first `tail` that follows
// `head`.
func insertBefore(t *testing.T, src, head, tail, text string) string {
	t.Helper()
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("coreutils/lib/gnu.fern no longer contains `%s`; update this gate's anchor", head)
	}
	j := strings.Index(src[i:], tail)
	if j < 0 {
		t.Fatalf("coreutils/lib/gnu.fern: no `%s` after `%s`", tail, head)
	}
	at := i + j
	return src[:at] + text + src[at:]
}

func runPatchedUniq(t *testing.T, bin string, args []string) outcome {
	t.Helper()
	argv := crossArgv(bin, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s %s never ran", bin, strings.Join(args, " "))
	}
	res := outcome{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exit: cmd.ProcessState.ExitCode()}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		res.signal = ws.Signal().String()
	}
	return res
}
