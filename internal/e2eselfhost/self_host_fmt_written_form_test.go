package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	goparser "github.com/jakechampion/lang/internal/parser"
	goprinter "github.com/jakechampion/lang/internal/printer"
)

// TestSelfHostFmtWrittenFormViaInterp gates the self-host formatter on every
// host, by driving examples/self_host/fern.fern through the native `-interp`
// instead of building an x86-64 driver.
//
// Every other self-host `-fmt` gate is suffixed X86_64 and needs a cross gcc
// plus native execution of the linked binary, so all of them skip on a
// non-x86-64 host — the shapes in #6802 were reachable in ~0.35 s per file from
// any machine the whole time (`-fmt` loads no stdlib), and nothing was checking
// them there.
//
// It states the two properties that see data loss, per #6838:
//
//   - STRUCTURAL: the self-host's output, re-parsed, is the same program as the
//     input. Byte-parity against native only catches a leak where native is
//     right, and a type-check of the output passes on output that still compiles
//     while having lost information.
//   - byte-parity with native, which is what pins layout.
//
// plus idempotence and the type-check property, so a case where both formatters
// agree on something broken still fails.
func TestSelfHostFmtWrittenFormViaInterp(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../examples/self_host/fern.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}
	dir := t.TempDir()

	selfHostFmt := func(t *testing.T, path string) string {
		t.Helper()
		cmd := exec.Command(interpBin, "-interp", driver, "--", "-fmt", path)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("self-host -fmt %s: %v (out: %s)", path, err, out)
		}
		return string(out)
	}

	for _, tc := range fmtParityCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got := selfHostFmt(t, path)

			// The desugar leaks #6802 names are each a marker a reader of the
			// issue would grep for, and they are checked by name because the
			// properties below report a shape diff rather than the cause.
			for _, leak := range []string{"try_", "__discard_", "__forc_", "/*unknown"} {
				if strings.Contains(got, leak) && !strings.Contains(tc.src, leak) {
					t.Errorf("self-host -fmt wrote the desugar's %q into the source:\n%s", leak, got)
				}
			}

			// Structural: same program in, same program out.
			in, err := goparser.Parse(tc.src)
			if err != nil {
				t.Fatalf("case source does not parse: %v", err)
			}
			back, err := goparser.Parse(got)
			if err != nil {
				t.Fatalf("self-host -fmt output does not parse: %v\n%s", err, got)
			}
			if want, have := goprinter.ASTShape(in), goprinter.ASTShape(back); want != have {
				t.Errorf("self-host -fmt changed the program\n%s", goprinter.FirstShapeDiff(want, have))
			}

			// Layout: byte-identical to native.
			want := goprinter.Format(in)
			if got != want {
				t.Errorf("self-host -fmt differs from native -fmt\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}

			fmtOutputTypeChecks(t, "self-host -fmt", tc.src, got)

			outPath := filepath.Join(dir, tc.name+"_fmt.fern")
			if err := os.WriteFile(outPath, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			if again := selfHostFmt(t, outPath); again != got {
				t.Errorf("self-host -fmt is not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
			}
		})
	}
}

// TestSelfHostFmtWriteAndDiffViaInterp pins #6804: the self-host driver exited 2
// on `-w` and `-d`, so `-fmt` could only reach stdout.
//
// `-w` was held back until #6802 — a formatter that emits source which does not
// compile turns an in-place write into a destroyed file — so the assertions here
// are as much about the ORDER of the two fixes as about the flags: the written
// file has to still be the same program, and a second `-w` has to be a no-op.
//
// `-d`'s output is compared byte-for-byte with native's, since a diff both tools
// print differently is not usable in a pre-commit hook that may call either.
func TestSelfHostFmtWriteAndDiffViaInterp(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../examples/self_host/fern.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	// Deliberately mis-laid-out, so every mode has something to report.
	const unformatted = `function add(a: i32,b: i32): i32 {
return a+b;
}

function main(): i32 {
    var t: i32   = add(1,2);
  if (t != 3) {
        return 1;
  }
  return 0;
}
`
	prog, err := goparser.Parse(unformatted)
	if err != nil {
		t.Fatalf("case source does not parse: %v", err)
	}
	want := goprinter.Format(prog)

	run := func(t *testing.T, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(interpBin, append([]string{"-interp", driver, "--"}, args...)...)
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	t.Run("write-back", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "w.fern")
		if err := os.WriteFile(path, []byte(unformatted), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, code := run(t, "-fmt", "-w", path); code != 0 {
			t.Fatalf("-fmt -w exited %d, want 0 (out: %s)", code, out)
		} else if out != "" {
			t.Errorf("-fmt -w wrote to stdout as well as the file: %q", out)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("-fmt -w wrote something other than the formatted source\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
		// Same program in the file afterwards, not merely a compiling one.
		back, err := goparser.Parse(string(got))
		if err != nil {
			t.Fatalf("the written-back file does not parse: %v", err)
		}
		if a, b := goprinter.ASTShape(prog), goprinter.ASTShape(back); a != b {
			t.Errorf("-fmt -w changed the program\n%s", goprinter.FirstShapeDiff(a, b))
		}
		if _, code := run(t, "-fmt", "-w", path); code != 0 {
			t.Fatalf("second -fmt -w exited %d, want 0", code)
		}
		again, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(got) {
			t.Errorf("-fmt -w is not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
		}
	})

	t.Run("diff", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "d.fern")
		if err := os.WriteFile(path, []byte(unformatted), 0o644); err != nil {
			t.Fatal(err)
		}
		got, code := run(t, "-fmt", "-d", path)
		if code != 1 {
			t.Errorf("-fmt -d on a file that needs formatting exited %d, want 1", code)
		}
		if want := goprinter.UnifiedDiff(unformatted, want, path, path); got != want {
			t.Errorf("-fmt -d differs from native's\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
		}
		// `-d` is read-only: the file it reported on is untouched.
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != unformatted {
			t.Errorf("-fmt -d rewrote the file it was asked to diff:\n%s", after)
		}

		clean := filepath.Join(dir, "clean.fern")
		if err := os.WriteFile(clean, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, code := run(t, "-fmt", "-d", clean); code != 0 || out != "" {
			t.Errorf("-fmt -d on already-formatted source = %q, exit %d; want empty, exit 0", out, code)
		}
	})
}
