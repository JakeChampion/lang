package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIoErrorIRWasm is the wasm leg of #4624 item 2: the wasm fs runtime
// helpers used to box their Err/Some payload as the raw WASI error CODE, so a
// program that matched the IoError value (not just Ok/Err) read an integer as an
// enum box and dispatched wrong / dereferenced garbage. Every preview-1 fs
// helper's error path now routes the errno through $__fern_build_io_error (the
// wasm sibling of native wasmbin's __build_io_error and the register backends'
// __fern_io_error): a WASI errno maps to a real IoError variant box
// ([variant_id@0][path@8]) carrying the offending path, so the match binds a
// well-formed variant + path string.
//
// Paths are RELATIVE and resolve against the preopen (the temp dir, mapped to
// guest / with CWD = temp dir): WASI is capability-sandboxed, so an absolute
// path is rejected with ENOTCAPABLE rather than the ENOENT the register/native
// backends see — the shared absolute-path ioErrorCases can't run verbatim here.
// A genuinely-missing relative path gives ENOENT(44) -> NotFound, matching the
// native interpreter's semantics for the same operation.
func TestSelfHostIoErrorIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host io-error wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A known-good file for the success controls ("hello\n" = 6 bytes).
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write ok file: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// "missing-fern-probe" is 18 chars — the length a NotFound(path) payload must
	// report, proving the path box is well-formed (not a raw errno / NULL).
	const missing = "missing-fern-probe"
	const missingLen = 18

	cases := []struct {
		name string
		src  string
		want int
	}{
		// read_file's Err payload matches the NotFound variant (the #4624 repro:
		// matching the IoError value, not just Ok/Err).
		{"read-notfound-variant", `function main(): i32 { match (read_file("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } } } return 0; }`, 2},
		// The NotFound payload is a well-formed string box carrying the path.
		{"read-notfound-payload-len", `function main(): i32 { match (read_file("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, missingLen},
		// Success path unchanged by the error-path rework.
		{"read-ok-control", `function main(): i32 { match (read_file("ok.txt")) { Ok(s) => { return s.len(); }, Err(_) => { return 90; } } return 0; }`, 6},
		// write_file into a missing parent dir -> ENOENT -> Some(NotFound), not NULL.
		{"writefile-notfound", `function main(): i32 { match (write_file("nodir-fern/x.txt", "hi")) { Some(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, None => { return 1; } } return 0; }`, 2},
		// write_file success still returns None.
		{"writefile-ok-control", `function main(): i32 { match (write_file("out-fern.txt", "hi")) { Some(_) => { return 90; }, None => { return 1; } } return 0; }`, 1},
		// stat's Err payload carries NotFound(path) with the right length.
		{"stat-notfound-payload-len", `function main(): i32 { match (stat("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, missingLen},
		// remove_file's Some payload matches NotFound.
		{"removefile-notfound", `function main(): i32 { match (remove_file("` + missing + `")) { Some(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, None => { return 1; } } return 0; }`, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed to compile %s: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_build_io_error")) {
				t.Fatalf("%s: WAT has no call $__fern_build_io_error (error path not routed through the IoError builder)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: wasm IR exit = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
