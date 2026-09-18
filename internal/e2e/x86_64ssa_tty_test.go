package e2e

import (
	"testing"
)

// The last group of builtins x86_64ssa had no emitter for (#9559): the
// window-size pair, the termios pair, their four Reader methods, and
// set_file_times. With these the backend builds every coreutil the catalogue
// had at the time; chroot arrived after, with four credential builtins of its
// own, and internal/e2e/chroot_creds_test.go is where those are pinned.
//
// Nothing in a test harness is a terminal, so the terminal half is asserted on
// the refusal: every query must answer ENOTTY rather than a value, and must
// refuse identically through the free function and through the Reader method,
// which is what says the method reaches the same body. termios_set is checked
// on the two arguments it validates BEFORE the ioctl - a wrong-length word
// array and an action past TCSETSF - so those answers hold without a terminal.
//
// set_file_times is the half that has an observable answer: a time written and
// read back through stat, and the omit bit leaving the half it names alone.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSATtySrc = `function main(): i32 {
    var base: string = getcwd();

    // Nothing in a test harness is a terminal, so every terminal query must
    // refuse with ENOTTY rather than answer, and refuse identically through
    // the free function and through the Reader method.
    match (window_size(0)) { Ok(s) => { return 10; }, Err(e) => {} }
    match (stdin().window_size()) { Ok(s) => { return 11; }, Err(e) => {} }
    match (termios_get(0)) { Ok(v) => { return 12; }, Err(e) => {} }
    match (stdin().termios_get()) { Ok(v) => { return 13; }, Err(e) => {} }
    match (set_window_size(0, 24, 80)) { Ok(_) => { return 14; }, Err(e) => {} }
    match (stdin().set_window_size(24, 80)) { Ok(_) => { return 15; }, Err(e) => {} }

    // termios_set validates before it asks the kernel: a wrong-length word
    // array and an out-of-range action are both EINVAL, and reach that answer
    // without a terminal.
    var short: i64[] = [1i64, 2i64];
    match (termios_set(0, 0, short)) { Ok(_) => { return 20; }, Err(e) => {} }
    var full: i64[] = [];
    var i: i32 = 0;
    while (i < 24) { full = full.append(0i64); i = i + 1; }
    match (termios_set(0, 9, full)) { Ok(_) => { return 21; }, Err(e) => {} }
    match (termios_set(0, 0, full)) { Ok(_) => { return 22; }, Err(e) => {} }
    match (stdin().termios_set(0, full)) { Ok(_) => { return 23; }, Err(e) => {} }

    // set_file_times: a fixed time lands in stat, and the omit bit leaves the
    // half it names alone.
    match (write_file(base + "/t", "x")) { Ok(_) => {}, Err(e) => { return 30; } }
    match (set_file_times(base + "/t", 1000000000i64, 0i64, 2000000000i64, 0i64, 0)) {
        Ok(_) => {}, Err(e) => { return 31; }
    }
    match (stat(base + "/t")) {
        Ok(s) => {
            if (s.atime != 1000000000i64) { return 32; }
            if (s.mtime != 2000000000i64) { return 33; }
        },
        Err(e) => { return 34; }
    }
    // Bit 1 omits the access time, so only the modification time moves.
    match (set_file_times(base + "/t", 0i64, 0i64, 3000000000i64, 0i64, 2)) {
        Ok(_) => {}, Err(e) => { return 35; }
    }
    match (stat(base + "/t")) {
        Ok(s) => {
            if (s.atime != 1000000000i64) { return 36; }
            if (s.mtime != 3000000000i64) { return 37; }
        },
        Err(e) => { return 38; }
    }
    match (set_file_times(base + "/no-such-file", 0i64, 0i64, 0i64, 0i64, 0)) {
        Ok(_) => { return 39; }, Err(e) => {}
    }
    return 0;
}
`

func TestX86_64SSATtyAndFileTimesMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "tty", "flat", x86SSATtySrc, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "tty", "ssa", x86SSATtySrc, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
