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
	// Nothing has crossed the rc==1 cliff yet: xs held the only reference all
	// the way through, so every append grew it in place.
	if (__arr_push_shared_count() != 0) { return 93; }
	if (__arr_push_shared_bytes() != (0 as i64)) { return 94; }
	// Now make a second reference and append through it. The buffer still has
	// spare capacity, so the copy that follows is bought by the extra
	// reference alone — which is the crossing the tally counts.
	var ys: i32[] = xs;
	ys = ys.append(999);
	if (__arr_push_shared_count() < 1) { return 95; }
	if (__arr_push_shared_bytes() < (4 as i64)) { return 96; }
	// xs must not see the appended element: a crossing that mutated in place
	// instead of copying would leave the two sharing a buffer.
	if (xs.len() != 43) { return 97; }
	if (ys.len() != 44) { return 98; }
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
		// Pre-created WITHOUT the exec bit, so openat's O_CREAT mode never
		// applies and only write_file_exec's fchmod can make it runnable —
		// which is the whole of what separates it from write_file. Against a
		// build that dropped the fchmod this comes back 0644.
		if err := os.WriteFile(written, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
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

// The five more that coreutils reaches: environ, getuid, getgid, getgroups
// and now_ns. Same argument as the four above — each had no emitter, so on
// arm64-linux's new default the programs calling them stopped compiling —
// and the same assertion: the two emitters must agree.
//
// Every value here is drawn from the running process, so nothing is pinned to
// a constant the source could hard-code. What the harness pins instead is the
// environment: the child is given exactly two variables, so environ() must
// answer a vector of exactly two non-empty strings — a stub returning an empty
// array, or one that read the wrong vector, cannot.
//
// The rest are invariants that hold whatever the process is: uid and gid are
// non-negative, getgroups answers an array, and now_ns is past 2020 and no
// earlier than a reading taken before it.
const arm64SSAProcessHelpersSource = `function main(): i32 {
	if (getuid() < 0) { return 80; }
	if (getgid() < 0) { return 81; }
	if (getgroups().len() < 0) { return 82; }
	var env: string[] = environ();
	if (env.len() != 2) { return 83; }
	if (env[0].len() == 0) { return 84; }
	if (env[1].len() == 0) { return 85; }
	var t0: i64 = now_ns();
	var t1: i64 = now_ns();
	if (t1 < t0) { return 86; }
	if (t0 < (1600000000000000000 as i64)) { return 87; }
	return 42;
}
`

func TestArm64SSAProcessHelpers(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	if qemu == "" {
		t.Skip("qemu-aarch64 is not on PATH")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "process.fern")
	if err := os.WriteFile(src, []byte(arm64SSAProcessHelpersSource), 0o644); err != nil {
		t.Fatal(err)
	}
	code := map[string]int{}
	for _, backend := range []string{"ssa", "flat"} {
		bin := filepath.Join(dir, backend)
		if o, err := exec.Command(fern, "-target", "arm64-linux", "-backend", backend, "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile -backend %s: %v\n%s", backend, err, o)
		}
		cmd := exec.Command(qemu, bin)
		cmd.Env = []string{"FERN_HELPER_PROBE=1", "PATH=/usr/bin"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		code[backend] = cmd.ProcessState.ExitCode()
	}
	if code["ssa"] != 42 {
		t.Errorf("-backend ssa exited %d, want 42 — the code names the step", code["ssa"])
	}
	if code["ssa"] != code["flat"] {
		t.Errorf("the two arm64 emitters disagree: ssa=%d flat=%d", code["ssa"], code["flat"])
	}
}
