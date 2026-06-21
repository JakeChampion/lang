package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWriteFileIRX86_64 routes the built-in write_file(path, content)
// through the self-hosted x86-64 IR path (newly added op_write_file, the write
// sibling of op_read_file) and verifies: routing is pinned to "ir", the compiled
// binary's exit matches the interpreter oracle, AND the bytes actually landed in
// the file. write_file returns Option[IoError] (None on success), reusing the
// existing __fern_write_file runtime. x86-64 only — no wasm file I/O
// (wasm_eligible rejects write_file, like read_file). This was the last non-libm
// CLI bail (tee); with it the whole CLI corpus except bc routes IR.
func TestSelfHostWriteFileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-writing driver test runs only natively")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	cases := []struct {
		name string
		// src is the content as written in the .fern string literal (with \n
		// escapes); want is the actual bytes that should land in the file.
		src  string
		want string
	}{
		{"short", "hello", "hello"},
		{"empty", "", ""},
		{"multiline", `line1\nline2\nline3\n`, "line1\nline2\nline3\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Distinct output paths for the interpreter oracle vs the compiled binary.
			interpOut := filepath.Join(t.TempDir(), "interp.out")
			binOut := filepath.Join(t.TempDir(), "bin.out")
			mk := func(path string) string {
				return "function main(): i32 { match (write_file(\"" + path + "\", \"" + tc.src + "\")) { Some(_) => { return 1; }, None => { return 0; } } }\n"
			}

			// Oracle: interpret, writing to interpOut.
			want := interpExit(t, interpBin, mk(interpOut))
			gotInterp, _ := os.ReadFile(interpOut)
			if string(gotInterp) != tc.want {
				t.Fatalf("interp wrote %q, want %q", gotInterp, tc.want)
			}

			// Routing must be IR (probe the binOut variant — path text doesn't affect routing).
			src := []byte(mk(binOut))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}

			// Compile + run, writing to binOut.
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
			got, err := os.ReadFile(binOut)
			if err != nil {
				t.Fatalf("read written file: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("%s wrote %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
