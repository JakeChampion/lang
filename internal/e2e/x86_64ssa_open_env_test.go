package e2e

import (
	"testing"
)

// The fourth group of builtins x86_64ssa had no emitter for (#9559): the rest
// of the open family, the environment vector, and the free line reader.
//
// open_appender and open_exclusive are the existing open helper at different
// openat flags. open_reader_with and open_writer_with take the flags from the
// caller instead - bit 0 is O_CREAT, bit 1 is O_NONBLOCK - so the probe checks
// that bit 0 is what decides whether a missing name is created or refused.
// environ is args() over envp, and read_line is Reader.read_line over standard
// input rather than a handle, so both must agree with the helper they share a
// body with: env() has to find the name environ() reports, and the line reader
// has to keep the newline.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSAOpenEnvSrc = `import "std/string";

function main(): i32 {
    var base: string = getcwd();

    // open_appender: O_APPEND, so a second open writes past what is there.
    match (write_file(base + "/a", "one")) { Ok(_) => {}, Err(e) => { return 10; } }
    var ap: Writer = match (open_appender(base + "/a")) { Ok(h) => h, Err(e) => { return 11; } };
    match (ap.write("two")) { Some(e) => { return 12; }, None => {} }
    match (ap.close()) { Some(e) => { return 13; }, None => {} }
    match (read_file(base + "/a")) { Ok(c) => { if (c != "onetwo") { return 14; } }, Err(e) => { return 15; } }

    // open_exclusive: creates, and refuses a name that already exists.
    var ex: Writer = match (open_exclusive(base + "/x")) { Ok(h) => h, Err(e) => { return 20; } };
    match (ex.write("new")) { Some(e) => { return 21; }, None => {} }
    match (ex.close()) { Some(e) => { return 22; }, None => {} }
    match (open_exclusive(base + "/x")) { Ok(h) => { return 23; }, Err(e) => {} }

    // open_writer_with: bit 0 is O_CREAT. Without it a missing name fails;
    // with it the file is created.
    match (open_writer_with(base + "/w", 0)) { Ok(h) => { return 30; }, Err(e) => {} }
    var ww: Writer = match (open_writer_with(base + "/w", 1)) { Ok(h) => h, Err(e) => { return 31; } };
    match (ww.write("made")) { Some(e) => { return 32; }, None => {} }
    match (ww.close()) { Some(e) => { return 33; }, None => {} }
    match (read_file(base + "/w")) { Ok(c) => { if (c != "made") { return 34; } }, Err(e) => { return 35; } }

    // open_reader_with: reads what is there, and refuses a missing name even
    // with the nonblock bit set.
    var rw: Reader = match (open_reader_with(base + "/w", 2)) { Ok(h) => h, Err(e) => { return 40; } };
    match (rw.read_chunk(16)) { Ok(s) => { if (s != "made") { return 41; } }, Err(e) => { return 42; } }
    match (rw.close()) { Some(e) => { return 43; }, None => {} }
    match (open_reader_with(base + "/no-such-name", 2)) { Ok(h) => { return 44; }, Err(e) => {} }

    // environ: memoised, and every entry is NAME=VALUE. env() must agree with
    // what environ reports for the same name.
    var e1: string[] = environ();
    var e2: string[] = environ();
    if (e1.len() != e2.len()) { return 50; }
    if (e1.len() < 1) { return 51; }
    var i: i32 = 0;
    while (i < e1.len()) {
        if (!e1[i].contains("=")) { return 52; }
        if (e1[i] != e2[i]) { return 53; }
        i = i + 1;
    }
    // env() must find the name environ() reports, which is what says the two
    // helpers read the same vector.
    var first: string = e1[0];
    match (env(first.slice_snap(0, first.index_of("=")))) { Some(v) => {}, None => { return 54; } }

    // read_line: standard input, one line at a time, the newline kept.
    match (read_line()) { Some(l) => { if (l != "alpha\n") { return 60; } }, None => { return 61; } }
    match (read_line()) { Some(l) => { if (l != "beta") { return 62; } }, None => { return 63; } }
    match (read_line()) { Some(l) => { return 64; }, None => {} }
    return 0;
}
`

func TestX86_64SSAOpenAndEnvMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	// Two lines, the second unterminated, so read_line is asked for a line
	// with a newline, a line without one, and then for one that is not there.
	const stdin = "alpha\nbeta"

	flat := runPathProbe(t, bin, qemu, dir, "openenv", "flat", x86SSAOpenEnvSrc, stdin)
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "openenv", "ssa", x86SSAOpenEnvSrc, stdin); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
