package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

func TestSelfHostFmtWrittenFormViaInterp(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/fern.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}
	dir := t.TempDir()

	selfHostFmtWrite := func(t *testing.T, paths []string) map[string]string {
		t.Helper()
		cmd := exec.Command(interpBin, append([]string{"-interp", driver, "--", "-fmt", "-w"}, paths...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if len(out) != 0 {
			t.Errorf("self-host -fmt -w wrote to stdout as well as the files: %q", out)
		}
		refused := map[string]string{}
		for _, line := range strings.Split(stderr.String(), "\n") {
			for _, p := range paths {
				if strings.HasPrefix(line, "fern: "+p+": ") {
					refused[p] = line
				}
			}
		}
		if err != nil && len(refused) == 0 {
			t.Fatalf("self-host -fmt -w over %d files: %v\n%s", len(paths), err, stderr.String())
		}
		return refused
	}
	readAll := func(t *testing.T, paths []string) []string {
		t.Helper()
		out := make([]string, len(paths))
		for i, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out[i] = string(b)
		}
		return out
	}

	paths := make([]string, len(fmtParityCases))
	for i, tc := range fmtParityCases {
		paths[i] = filepath.Join(dir, tc.name+".fern")
		if err := os.WriteFile(paths[i], []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := selfHostFmtWrite(t, paths)
	got := readAll(t, paths)
	refusedAgain := selfHostFmtWrite(t, paths)
	again := readAll(t, paths)

	for i, tc := range fmtParityCases {
		t.Run(tc.name, func(t *testing.T) {
			if line, ok := refused[paths[i]]; ok {
				t.Fatalf("self-host -fmt -w refused the case: %s", line)
			}
			got := got[i]

			for _, leak := range []string{"try_", "__discard_", "__forc_", "/*unknown"} {
				if strings.Contains(got, leak) && !strings.Contains(tc.src, leak) {
					t.Errorf("self-host -fmt wrote the desugar's %q into the source:\n%s", leak, got)
				}
			}

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

			want := goprinter.Format(in)
			if got != want {
				t.Errorf("self-host -fmt differs from native -fmt\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}

			fmtOutputTypeChecks(t, "self-host -fmt", tc.src, got)

			if line, ok := refusedAgain[paths[i]]; ok {
				t.Fatalf("self-host -fmt -w refused its own output: %s", line)
			}
			if again[i] != got {
				t.Errorf("self-host -fmt is not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again[i])
			}
		})
	}
}

func TestSelfHostFmtWriteAndDiffViaInterp(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/fern.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	const unformatted = `function add(a: i32,b: i32): i32 {
return a+b;
}

function main(): i32 {
    let t: i32   = add(1,2);
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

	t.Run("file-list", func(t *testing.T) {
		dir := t.TempDir()
		messy := filepath.Join(dir, "messy.fern")
		clean := filepath.Join(dir, "clean.fern")
		messy2 := filepath.Join(dir, "messy2.fern")
		for _, f := range []struct{ path, src string }{{messy, unformatted}, {clean, want}, {messy2, unformatted}} {
			if err := os.WriteFile(f.path, []byte(f.src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if out, code := run(t, "-fmt", messy, clean, messy2); code != 0 || out != want+want+want {
			t.Errorf("-fmt over three files: exit %d, out:\n%s", code, out)
		}
		wantDiff := goprinter.UnifiedDiff(unformatted, want, messy, messy) + goprinter.UnifiedDiff(unformatted, want, messy2, messy2)
		if out, code := run(t, "-fmt", "-d", messy, clean, messy2); code != 1 || out != wantDiff {
			t.Errorf("-fmt -d over three files: exit %d (want 1), out:\n%s", code, out)
		}
		if out, code := run(t, "-fmt", "-o", filepath.Join(dir, "out.fern"), messy, clean); code != 2 || out != "" {
			t.Errorf("-fmt -o with two inputs: exit %d (want 2), out %q", code, out)
		}
		if out, code := run(t, "-fmt", "-w", messy, clean, messy2); code != 0 || out != "" {
			t.Errorf("-fmt -w over three files: exit %d, out %q", code, out)
		}
		for _, p := range []string{messy, clean, messy2} {
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("-fmt -w left %s as:\n%s", filepath.Base(p), got)
			}
		}
	})
}
