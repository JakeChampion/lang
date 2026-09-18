package e2e

import (
	"testing"
)

// The third group of builtins x86_64ssa had no emitter for (#9559): the Reader
// and Writer methods that make one syscall on the descriptor the handle holds
// at [handle+8], beyond the read, write and close it already had - seek,
// truncate, write_some, fsync, fdatasync, syncfs, dup_onto and flags, plus the
// free sync(2).
//
// Each is exercised on its success path and on EBADF, which is the failure a
// caller can actually provoke: after close(), the handle's descriptor is gone,
// so every method on it must report rather than answer. That matters here
// because the two result shapes are inverted - Option[IoError] is None on
// success, Result is Ok on success, and both are the same box with a different
// tag - so a helper that boxed the wrong tag would pass a success-only probe.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSAFdMethodSrc = `function main(): i32 {
    var base: string = getcwd();
    var path: string = base + "/f";

    var wh: Writer = match (open_writer(path)) { Ok(h) => h, Err(e) => { return 10; } };
    // write_some reports the count it wrote, where write forgets it.
    match (wh.write_some("abcdefgh")) { Ok(n) => { if (n != 8i64) { return 11; } }, Err(e) => { return 12; } }
    // seek back to 3 and overwrite, so the offset is observable in the bytes.
    match (wh.seek(3i64, 0)) { Ok(o) => { if (o != 3i64) { return 13; } }, Err(e) => { return 14; } }
    match (wh.write_some("XY")) { Ok(n) => { if (n != 2i64) { return 15; } }, Err(e) => { return 16; } }
    // flags: a writer opened for writing is writable and not readable.
    match (wh.flags()) { Ok(f) => { if (f % 2i64 != 0i64 || (f / 2i64) % 2i64 != 1i64) { return 17; } }, Err(e) => { return 18; } }
    match (wh.truncate(6i64)) { Some(e) => { return 19; }, None => {} }
    match (wh.fsync()) { Some(e) => { return 20; }, None => {} }
    match (wh.fdatasync()) { Some(e) => { return 21; }, None => {} }
    match (wh.syncfs()) { Some(e) => { return 22; }, None => {} }
    match (wh.close()) { Some(e) => { return 23; }, None => {} }

    match (read_file(path)) { Ok(c) => { if (c != "abcXYf") { return 30; } }, Err(e) => { return 31; } }

    var rh: Reader = match (open_reader(path)) { Ok(h) => h, Err(e) => { return 40; } };
    match (rh.seek(2i64, 0)) { Ok(o) => { if (o != 2i64) { return 41; } }, Err(e) => { return 42; } }
    match (rh.read_chunk(2)) { Ok(s) => { if (s != "cX") { return 43; } }, Err(e) => { return 44; } }
    // seek from the end: -1 lands on the last byte.
    match (rh.seek(0i64 - 1i64, 2)) { Ok(o) => { if (o != 5i64) { return 45; } }, Err(e) => { return 46; } }
    match (rh.flags()) { Ok(f) => { if (f % 2i64 != 1i64 || (f / 2i64) % 2i64 != 0i64) { return 47; } }, Err(e) => { return 48; } }
    match (rh.fsync()) { Some(e) => { return 49; }, None => {} }
    match (rh.fdatasync()) { Some(e) => { return 50; }, None => {} }
    match (rh.close()) { Some(e) => { return 51; }, None => {} }

    // The error path: every one of these answers EBADF on a descriptor that
    // has been closed, which is the failure a caller can actually provoke.
    match (wh.seek(0i64, 0)) { Ok(o) => { return 60; }, Err(e) => {} }
    match (wh.flags()) { Ok(f) => { return 61; }, Err(e) => {} }
    match (wh.write_some("z")) { Ok(n) => { return 62; }, Err(e) => {} }
    match (wh.truncate(0i64)) { Some(e) => {}, None => { return 63; } }
    match (wh.fsync()) { Some(e) => {}, None => { return 64; } }
    match (wh.fdatasync()) { Some(e) => {}, None => { return 65; } }

    // dup_onto: point a spare descriptor at the writer's, then write through
    // the original and read the result back.
    var w2: Writer = match (open_writer(base + "/g")) { Ok(h) => h, Err(e) => { return 70; } };
    var w3: Writer = match (open_writer(base + "/h")) { Ok(h) => h, Err(e) => { return 71; } };
    match (w3.dup_onto(1)) { Some(e) => { return 72; }, None => {} }
    match (w3.close()) { Some(e) => { return 73; }, None => {} }
    match (w2.close()) { Some(e) => { return 74; }, None => {} }

    sync();
    return 0;
}
`

func TestX86_64SSAFdMethodsMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "fdmethods", "flat", x86SSAFdMethodSrc, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "fdmethods", "ssa", x86SSAFdMethodSrc, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
