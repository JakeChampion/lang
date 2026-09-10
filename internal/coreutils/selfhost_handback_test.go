package coreutils

import (
	"bytes"
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
// loop recycled it under the live Getopt. A twelfth field on Getopt is what
// put the two structs in one class, which is why the short spellings and
// every 11-field build were fine.
//
// The recipe is the issue's own: uniq compiled by the self-host against a
// `Getopt` carrying one more field, then a long option that matches. The
// tree's Getopt is copied, not edited — the field count is the trigger, not
// the fix, and the gate must keep tripping whatever gnu.fern declares.
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
	gnuPath := filepath.Join(work, "lib", "gnu.fern")
	gnu, err := os.ReadFile(gnuPath)
	if err != nil {
		t.Fatal(err)
	}
	decl := "operands: string[], past_dashdash: boolean }"
	init := "operands: empty, past_dashdash: false };"
	if !strings.Contains(string(gnu), decl) || !strings.Contains(string(gnu), init) {
		t.Fatalf("coreutils/lib/gnu.fern no longer declares Getopt the way this recipe patches it; update the two anchors")
	}
	patched := strings.Replace(string(gnu), decl, "operands: string[], past_dashdash: boolean, extra: i32 }", 1)
	patched = strings.Replace(patched, init, "operands: empty, past_dashdash: false, extra: 0 };", 1)
	if err := os.WriteFile(gnuPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "coreutils", "uniq.fern"))
	if err != nil {
		t.Fatal(err)
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
