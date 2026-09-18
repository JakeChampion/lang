package e2e

import (
	"testing"
)

// The fifth group of builtins x86_64ssa had no emitter for (#9559): what a
// process can ask about the system it runs on - read_dir_all, statfs,
// uname_field and getgroups.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSASysInfoSrc = `function main(): i32 {
    var base: string = getcwd();

    // read_dir_all keeps "." and "..", which read_dir drops; otherwise the
    // two must list the same names.
    match (create_dir(base + "/d", 493)) { Ok(_) => {}, Err(e) => { return 10; } }
    match (write_file(base + "/d/one", "x")) { Ok(_) => {}, Err(e) => { return 11; } }
    match (write_file(base + "/d/two", "y")) { Ok(_) => {}, Err(e) => { return 12; } }
    var all: string[] = match (read_dir_all(base + "/d")) { Ok(v) => v, Err(e) => { return 13; } };
    var some: string[] = match (read_dir(base + "/d")) { Ok(v) => v, Err(e) => { return 14; } };
    if (some.len() != 2) { return 15; }
    if (all.len() != 4) { return 16; }
    var dots: i32 = 0;
    var i: i32 = 0;
    while (i < all.len()) {
        if (all[i] == "." || all[i] == "..") { dots = dots + 1; }
        i = i + 1;
    }
    if (dots != 2) { return 17; }
    match (read_dir_all(base + "/no-such-dir")) { Ok(v) => { return 18; }, Err(e) => {} }

    // statfs: a mounted filesystem has a positive block size and at least as
    // many blocks as it has free, and PATH_MAX is the kernel's constant.
    match (statfs(base)) {
        Ok(s) => {
            if (s.block_size < 1i64) { return 20; }
            if (s.blocks < s.blocks_free) { return 21; }
            if (s.name_max < 1i64) { return 22; }
            if (s.path_max != 4096i64) { return 23; }
        },
        Err(e) => { return 24; }
    }
    match (statfs(base + "/no-such-path")) { Ok(s) => { return 25; }, Err(e) => {} }

    // uname_field: the five named fields are non-empty, and an index naming
    // no field is the empty string rather than whatever follows in the record.
    var f: i32 = 0;
    while (f < 5) {
        if (uname_field(f).len() < 1) { return 30; }
        f = f + 1;
    }
    if (uname_field(0) != "Linux") { return 31; }
    if (uname_field(5).len() != 0) { return 32; }
    if (uname_field(0 - 1).len() != 0) { return 33; }

    // getgroups: memoised, so two calls agree entry for entry, and no gid is
    // the kernel's -1 read through a bad widening.
    var g1: i64[] = getgroups();
    var g2: i64[] = getgroups();
    if (g1.len() != g2.len()) { return 40; }
    var j: i32 = 0;
    while (j < g1.len()) {
        if (g1[j] != g2[j]) { return 41; }
        if (g1[j] < 0i64) { return 42; }
        if (g1[j] > 4294967295i64) { return 43; }
        j = j + 1;
    }
    return 0;
}
`

func TestX86_64SSASysInfoMatchesTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "sysinfo", "flat", x86SSASysInfoSrc, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "sysinfo", "ssa", x86SSASysInfoSrc, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
