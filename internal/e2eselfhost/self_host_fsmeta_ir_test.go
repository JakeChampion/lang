package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The metadata writes on an entry that already exists (#9059) through the
// SELF-HOST IR path: `chmod` and `set_file_times`.
//
// Neither can be proved by compiling. A `chmod` that lowered to nothing, to
// the wrong syscall number, or with its two operands swapped still links and
// still returns Ok; so does a `set_file_times` that wrote the modification
// time into the access slot, dropped the nanosecond halves, or ignored the
// omit bits. So every assertion here reads the metadata BACK through `stat`
// and compares it exactly — the mode to twelve bits and each timestamp to the
// nanosecond.
//
// `internal/e2eselfhost` is PRIMARY for a self-host lowering change
// (docs/TEST-GATES.md): the fixpoint is self-referential and cannot see a
// stable miscompile of a construct the compiler does not itself use, and
// nothing in the compiler chmods or sets a timestamp.
//
// The two timestamps the probe writes are distinct from each other, distinct
// from any current clock reading, and both carry a non-zero nanosecond
// remainder — so a swap, a truncation to seconds, or a stale value all show up
// as a different number rather than as a plausible one.

// Two timestamps, neither round and neither close to now. atime is the older
// of the pair, so a swapped write puts the larger number in the smaller slot.
const (
	fsMetaATimeSec  = 1111111111
	fsMetaATimeNsec = 222333444
	fsMetaMTimeSec  = 1444555666
	fsMetaMTimeNsec = 777888999

	// The second round, which each omit leg half-writes.
	fsMetaATime2Sec  = 1666777888
	fsMetaATime2Nsec = 135792468
	fsMetaMTime2Sec  = 1777888999
	fsMetaMTime2Nsec = 864213579

	// 0o5755: setuid and sticky on top of rwxr-xr-x. A backend that masked
	// the mode to 0o777 before the syscall loses the top two and lands
	// 0o755 instead.
	fsMetaModeSuid = 0o5755
	// 0o640, with nothing above the nine permission bits.
	fsMetaModePlain = 0o640
)

// selfHostFsMetaSource is the probe, parameterised by the directory its paths
// resolve against — "" for the wasm leg, which runs under its preopen.
//
// `withChmod` is off for wasm: neither WASI preview has permission bits, so
// E066 refuses the builtin on that target (capability `fsmode`) and the wasm
// probe is the same program with that block removed.
//
// Every failure returns its own exit code, so the number names the step.
func selfHostFsMetaSource(prefix string, withChmod bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	src := fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "meta\n")) { Ok(_) => {}, Err(_) => { return 1; } }

    // Both timestamps, seconds and nanoseconds, read back exactly.
    match (set_file_times(%[1]q, %[3]d, %[4]d, %[5]d, %[6]d, 0)) { Ok(_) => {}, Err(_) => { return 2; } }
    match (stat(%[1]q)) {
        Ok(f) => {
            if (f.atime != (%[3]d as i64)) { return 3; }
            if (f.atime_nsec != (%[4]d as i64)) { return 4; }
            if (f.mtime != (%[5]d as i64)) { return 5; }
            if (f.mtime_nsec != (%[6]d as i64)) { return 6; }
        },
        Err(_) => { return 7; }
    }

    // Omit the access time (bit 1): the modification time moves to the second
    // round and the access time keeps what the call above wrote, even though
    // this call passed a different one.
    match (set_file_times(%[1]q, %[7]d, %[8]d, %[9]d, %[10]d, 2)) { Ok(_) => {}, Err(_) => { return 8; } }
    match (stat(%[1]q)) {
        Ok(f) => {
            if (f.atime != (%[3]d as i64)) { return 9; }
            if (f.atime_nsec != (%[4]d as i64)) { return 10; }
            if (f.mtime != (%[9]d as i64)) { return 11; }
            if (f.mtime_nsec != (%[10]d as i64)) { return 12; }
        },
        Err(_) => { return 13; }
    }

    // ...and the mirror, omitting the modification time (bit 2).
    match (set_file_times(%[1]q, %[7]d, %[8]d, %[5]d, %[6]d, 4)) { Ok(_) => {}, Err(_) => { return 14; } }
    match (stat(%[1]q)) {
        Ok(f) => {
            if (f.atime != (%[7]d as i64)) { return 15; }
            if (f.atime_nsec != (%[8]d as i64)) { return 16; }
            if (f.mtime != (%[9]d as i64)) { return 17; }
            if (f.mtime_nsec != (%[10]d as i64)) { return 18; }
        },
        Err(_) => { return 19; }
    }

    // Bit 0 writes the SYMLINK's own timestamps. Both halves matter: the link
    // moves and the file it names does not.
    match (create_symlink(%[11]q, %[12]q)) { Ok(_) => {}, Err(_) => { return 20; } }
    match (lstat(%[12]q)) { Ok(l) => { if (l.is_file) { return 21; } }, Err(_) => { return 22; } }
    match (set_file_times(%[12]q, %[3]d, %[4]d, %[3]d, %[4]d, 1)) { Ok(_) => {}, Err(_) => { return 23; } }
    match (lstat(%[12]q)) {
        Ok(l) => {
            if (l.mtime != (%[3]d as i64)) { return 24; }
            if (l.mtime_nsec != (%[4]d as i64)) { return 25; }
        },
        Err(_) => { return 26; }
    }
    match (stat(%[1]q)) { Ok(f) => { if (f.mtime != (%[9]d as i64)) { return 27; } }, Err(_) => { return 28; } }

    // A missing path is an Err naming the kind, not a silent Ok.
    match (set_file_times(%[2]q, %[3]d, %[4]d, %[5]d, %[6]d, 0)) {
        Ok(_) => { return 29; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 30; } } }
    }
`, p("meta.txt"), p("missing.txt"),
		fsMetaATimeSec, fsMetaATimeNsec, fsMetaMTimeSec, fsMetaMTimeNsec,
		fsMetaATime2Sec, fsMetaATime2Nsec, fsMetaMTime2Sec, fsMetaMTime2Nsec,
		"meta.txt", p("meta.link"))

	if withChmod {
		src += fmt.Sprintf(`    // chmod sets the low TWELVE bits verbatim — setuid, setgid and sticky
    // among them — and the umask is not consulted, because a mask filters a
    // creation and this is not one.
    match (chmod(%[1]q, %[3]d)) { Ok(_) => {}, Err(_) => { return 31; } }
    match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[3]d as u32)) { return 32; } }, Err(_) => { return 33; } }
    match (chmod(%[1]q, %[4]d)) { Ok(_) => {}, Err(_) => { return 34; } }
    match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[4]d as u32)) { return 35; } }, Err(_) => { return 36; } }
    match (chmod(%[2]q, 420)) {
        Ok(_) => { return 37; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 38; } } }
    }
`, p("meta.txt"), p("missing.txt"), fsMetaModeSuid, fsMetaModePlain)
	}
	src += `    return 0;
}
`
	return src
}

// selfHostFsMetaTree asserts what the probe left on disk, read through Go's
// own stat rather than the compiler's — so a `stat` that agreed with a broken
// `set_file_times` about the wrong value cannot make the probe self-consistent.
func selfHostFsMetaTree(t *testing.T, dir string, withChmod bool) {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "meta.txt"))
	if err != nil {
		t.Fatalf("stat meta.txt: %v", err)
	}
	if got, want := fi.ModTime().UnixNano(), int64(fsMetaMTime2Sec)*1e9+fsMetaMTime2Nsec; got != want {
		t.Errorf("meta.txt mtime = %d ns, want %d ns", got, want)
	}
	if withChmod {
		if perm := fi.Mode().Perm(); perm != fsMetaModePlain {
			t.Errorf("meta.txt mode = %o, want %o", perm, fsMetaModePlain)
		}
		// The chmod before the last one set setuid and sticky; both have to
		// be GONE, or the second chmod OR-ed where it should have replaced.
		if high := fi.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky); high != 0 {
			t.Errorf("meta.txt still carries %v after the final chmod", high)
		}
	}
	li, err := os.Lstat(filepath.Join(dir, "meta.link"))
	if err != nil {
		t.Fatalf("lstat meta.link: %v", err)
	}
	if got, want := li.ModTime().UnixNano(), int64(fsMetaATimeSec)*1e9+fsMetaATimeNsec; got != want {
		t.Errorf("meta.link mtime = %d ns, want %d ns — the nofollow bit wrote through the link",
			got, want)
	}
}

// TestSelfHostFsMetaIR is the x86-64 leg: the self-host driver emits the asm,
// gcc links it, and the program runs against a real temporary directory.
func TestSelfHostFsMetaIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("fsmeta test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	src := selfHostFsMetaSource(work, true)

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, sym := range []string{"__fern_chmod", "__fern_set_file_times"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("%s did not reach the IR runtime path (absent from the asm)", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "fsmeta_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("fsmeta program exited %d, want 0 — the code names the step (see selfHostFsMetaSource)", code)
	}
	selfHostFsMetaTree(t, work, true)
}

// TestSelfHostFsMetaIRArm64 is the same probe through the arm64 IR backend
// under qemu. The two syscall numbers differ from x86-64's and the operand
// reversal is a second hand-written sequence, so neither is taken on trust.
func TestSelfHostFsMetaIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("fsmeta test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	src := selfHostFsMetaSource(work, true)

	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, sym := range []string{"bl __fn___fern_chmod", "bl __fn___fern_set_file_times"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("no `%s` in the emitted asm — it did not lower through the arm64 IR path", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "fsmeta_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("fsmeta arm64 program exited %d, want 0 — the code names the step (see selfHostFsMetaSource)", code)
	}
	selfHostFsMetaTree(t, work, true)
}

// TestSelfHostFsMetaWasmIR is the wasm leg. `set_file_times` is a real WASI
// call (path_filestat_set_times), so it is exercised rather than classified
// out; `chmod` is absent, because neither preview has permission bits and E066
// refuses the builtin on that target.
//
// Preview 1 measures a timestamp in UNSIGNED nanoseconds since the epoch, so
// the pair is folded to one count on the way across and split back apart by
// the host — which is why the probe's nanosecond halves have to survive a
// multiply and an add rather than being carried in their own field.
func TestSelfHostFsMetaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fsmeta wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := selfHostFsMetaSource("", false)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_set_file_times")) {
		t.Fatal("set_file_times did not reach the wasm IR runtime path (no call $__fern_set_file_times in WAT)")
	}
	work := t.TempDir()
	watFile := filepath.Join(work, "fsmeta_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = work
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("fsmeta wasm program exited %d, want 0 — the code names the step\n--- WAT ---\n%s", code, wat)
	}
	selfHostFsMetaTree(t, work, false)
}
