package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The four runtime helpers arm64ssa had no emitter for until #9525: stdin's
// `read_line`, `write_file_exec`, and the two rc==1 cliff readers. Every one
// of them is reached by the self-host compiler, which is why a plain
// `fern -target arm64-linux examples/self_host/fern.fern` could not link once
// the SSA backend became that target's default.
//
// The probe returns 42 only if all four did their job, and the two emitters
// have to agree on every observable: the exit code, the file's contents, and
// its mode. A helper that links but answers wrongly passes a link check and
// fails here.
const arm64SSACLIHelpersSource = `function main(): i32 {
	match (read_line()) {
		None => { return 90; },
		Some(line) => {
			if (line.len() != 6) { return 91; }
			match (write_file_exec(args()[1], line)) {
				Err(_) => { return 92; },
				Ok(_) => {}
			}
		}
	}
	var xs: i32[] = [1, 2, 3];
	var i: i32 = 0;
	while (i < 40) { xs = xs.append(i); i = i + 1; }
	if (__arr_push_shared_count() < 0) { return 93; }
	if (__arr_push_shared_bytes() < (0 as i64)) { return 94; }
	return 42;
}
`

func TestArm64SSACLIRuntimeHelpers(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	if qemu == "" {
		t.Skip("qemu-aarch64 is not on PATH")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "helpers.fern")
	if err := os.WriteFile(src, []byte(arm64SSACLIHelpersSource), 0o644); err != nil {
		t.Fatal(err)
	}
	type result struct {
		code int
		body string
		mode os.FileMode
	}
	got := map[string]result{}
	for _, backend := range []string{"ssa", "flat"} {
		bin := filepath.Join(dir, backend)
		if o, err := exec.Command(fern, "-target", "arm64-linux", "-backend", backend, "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile -backend %s: %v\n%s", backend, err, o)
		}
		written := filepath.Join(dir, backend+".out")
		cmd := exec.Command(qemu, bin, written)
		cmd.Stdin = strings.NewReader("hello\n")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		st, err := os.Stat(written)
		if err != nil {
			t.Fatalf("-backend %s wrote no file: %v (exit %d)\n%s", backend, err, cmd.ProcessState.ExitCode(), stderr.String())
		}
		body, err := os.ReadFile(written)
		if err != nil {
			t.Fatal(err)
		}
		got[backend] = result{cmd.ProcessState.ExitCode(), string(body), st.Mode().Perm()}
	}
	// 42 means every step passed; any other value names the step (see the
	// source above), so report it rather than only that the two differ.
	if got["ssa"].code != 42 {
		t.Errorf("-backend ssa exited %d, want 42 — the code names the step", got["ssa"].code)
	}
	if got["ssa"] != got["flat"] {
		t.Errorf("the two arm64 emitters disagree:\n  ssa:  %+v\n  flat: %+v", got["ssa"], got["flat"])
	}
	// write_file_exec exists to make the file runnable, which is the whole of
	// what separates it from write_file.
	if got["ssa"].mode&0o111 == 0 {
		t.Errorf("write_file_exec left mode %#o, which is not executable", got["ssa"].mode)
	}
}
