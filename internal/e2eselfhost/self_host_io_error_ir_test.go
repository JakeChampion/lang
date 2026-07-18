package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type ioErrorCase struct {
	name string
	src  string
	want int
}

// ioErrorCases builds the #4370 error-payload case set: the segfault repro
// (matching the Err variant), a payload-length case proving the path string
// box is well-formed, write_file's ENOENT shape, and success-path controls
// for both helpers. Absolute temp paths work under qemu-user too (syscalls
// pass through to the host).
func ioErrorCases(t *testing.T) []ioErrorCase {
	t.Helper()
	okPath := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(okPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write ok file: %v", err)
	}
	// A regular file used as a directory component makes stat(2) fail with
	// ENOTDIR (20) — a non-ENOENT errno that must classify as Other, not
	// NotFound. This is the regression guard for stat's errno→IoError mapping:
	// the x86-64 IR path's Fern __fern_stat maps the full errno set (matching
	// native + the arm64 hand-asm), where an earlier cut flattened every
	// failure to NotFound. Root-safe (ENOTDIR isn't a permission check).
	regFile := filepath.Join(t.TempDir(), "reg.txt")
	if err := os.WriteFile(regFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write reg file: %v", err)
	}
	notDir := regFile + "/child"
	return []ioErrorCase{
		// The issue's repro: matching the Err payload's variant.
		{"notfound-variant", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } } } return 0; }`, 2},
		// The payload itself is a well-formed string box carrying the path.
		{"notfound-payload-len", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		// Success path unchanged by the error-path rework.
		{"ok-control", `function main(): i32 { match (read_file("` + okPath + `")) { Ok(s) => { return s.len(); }, Err(_) => { return 90; } } return 0; }`, 6},
		// write_file's Some(IoError) payload: an open failure (missing parent
		// dir -> ENOENT) must carry NotFound, not NULL.
		{"writefile-notfound", `function main(): i32 { match (write_file("/nonexistent-dir-fern/x.txt", "hi")) { Some(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, None => { return 1; } } return 0; }`, 2},
		// write_file success still returns None (the error-path rework touched
		// the shared epilogue on both backends).
		{"writefile-ok-control", `function main(): i32 { match (write_file("` + filepath.Join(t.TempDir(), "out.txt") + `", "hi")) { Some(_) => { return 90; }, None => { return 1; } } return 0; }`, 1},
		// stat / remove_file error payloads (the same NULL-payload class).
		{"stat-notfound-payload-len", `function main(): i32 { match (stat("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		// stat ENOTDIR (non-ENOENT errno) → Other, NOT NotFound. Guards the full
		// errno→IoError mapping in the Fern __fern_stat (x86-64 IR); matches native
		// interp + the arm64 hand-asm.
		{"stat-notdir-is-other", `function main(): i32 { match (stat("` + notDir + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _) => { return 5; }, NotFound(_) => { return 6; }, _ => { return 7; } } } } return 0; }`, 5},
		{"removefile-notfound", `function main(): i32 { match (remove_file("/nonexistent-fern-probe")) { Some(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, None => { return 1; } } return 0; }`, 2},
	}
}

// TestSelfHostIoErrorIRX86_64 pins #4370: the fs helpers' error paths used to
// box Err with a NULL payload, so a program that matched the IoError value
// (rather than just Ok/Err) dereferenced 0 and segfaulted on the self-host
// x86 IR path. __fern_read_file's error path now routes through
// __fern_io_error (the self-host sibling of native's helper): errno maps to a
// real variant box ([interned-shape@0, payload@8...]) carrying the path.
// Each case is routing-pinned to "ir" and its expectation is the native
// interpreter's result.
func TestSelfHostIoErrorIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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

	cases := ioErrorCases(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s: binary did not exit normally (segfault?)", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostIoErrorIRArm64 runs the same cases through the arm64 IR path
// (asm_ir_run -target arm64 -ir), whose runtime helpers live in
// asm_arm64.fern and carried the same NULL Err/Some payloads (#4624, the
// arm64 leg of #4370). The read_file/write_file error paths now route through
// the arm64 __fern_io_error. CI-gated arm64 (qemu).
func TestSelfHostIoErrorIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm.fern", "asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ioErrorCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(asm, []byte("__fern_io_error")) {
				t.Fatalf("%s: emitted asm has no __fern_io_error — the error-path rework is not wired", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ioe_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally (segfault?)", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
