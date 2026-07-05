package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

	// Ok-path control: a real file whose contents length is the expected exit.
	okPath := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(okPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write ok file: %v", err)
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		// The issue's repro: matching the Err payload's variant.
		{"notfound-variant", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } } } return 0; }`, 2},
		// The payload itself is a well-formed string box carrying the path.
		{"notfound-payload-len", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		// Success path unchanged by the error-path rework.
		{"ok-control", `function main(): i32 { match (read_file("` + okPath + `")) { Ok(s) => { return s.len(); }, Err(_) => { return 90; } } return 0; }`, 6},
		// write_file's Some(IoError) payload: an open failure (missing parent
		// dir -> ENOENT) must carry NotFound, not NULL.
		{"writefile-notfound", `function main(): i32 { match (write_file("/nonexistent-dir-fern/x.txt", "hi")) { Some(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, None => { return 1; } } return 0; }`, 2},
		// write_file success still returns None (the error-path rework added a
		// stack slot the success epilogue must also pop).
		{"writefile-ok-control", `function main(): i32 { match (write_file("` + filepath.Join(t.TempDir(), "out.txt") + `", "hi")) { Some(_) => { return 90; }, None => { return 1; } } return 0; }`, 1},
	}
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
