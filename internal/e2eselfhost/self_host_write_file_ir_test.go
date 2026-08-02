package e2eselfhost

import (
	"bytes"
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
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
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

// TestSelfHostWriteFileIRWasm routes write_file(path, content) through the wasm
// IR backend under wasmtime, granting the run directory as preopen fd 3
// (`--dir=.::/`). write_file now lowers on the wasm IR path: wasm_ir emits
// `call $__fern_write_file` and wasm_ir_run pulls in the path_open / fd_close
// imports (plus the fd_write import its gate now covers) + the writefile_func
// helper. Each case writes to wf_out.txt and the test reads the bytes back from
// the host run directory, asserting None (exit 0) and the exact content landed.
func TestSelfHostWriteFileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host write_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name, src, want string
	}{
		{"short", "hello", "hello"},
		{"empty", "", ""},
		{"multiline", `line1\nline2\nline3\n`, "line1\nline2\nline3\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outPath := "wf_" + tc.name + ".txt"
			src := []byte("function main(): i32 { match (write_file(\"" + outPath + "\", \"" + tc.src + "\")) { Some(_) => { return 1; }, None => { return 0; } } }\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_write_file")) {
				t.Fatalf("%s: no `call $__fern_write_file` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "wf_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("write_file wasm IR %s: exit %d, want 0 (None)", tc.name, code)
			}
			got, err := os.ReadFile(filepath.Join(dir, outPath))
			if err != nil {
				t.Fatalf("%s: read written file: %v", tc.name, err)
			}
			if string(got) != tc.want {
				t.Errorf("write_file wasm IR %s: wrote %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
