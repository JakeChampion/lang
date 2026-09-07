package e2e

import "testing"

// `open_exclusive` creates at 0600, not 0644.
//
// Its whole purpose (#8776) is a file the caller is the sole creator of, and
// its first consumer — `split -n` spooling a pipe it cannot seek — writes the
// user's input into it. At 0644 that input is world-readable for as long as
// the spool exists, which is the disclosure the primitive was added to avoid;
// gnulib's create_temp_file uses 0600 for the same reason. The other two
// openers keep 0644: they create files the user asked for by name, where the
// umask is the right authority.
//
// Pinned because the mode is a bare number in five emitters (three native
// tables, and the self-host's two asm backends, which key it off the
// canonical x86-64 flag word 193), so nothing else would notice it drifting
// back.
// The name differs per backend: the two runs share a working directory, and
// O_EXCL would (correctly) refuse the second one a name the first created.
func openExclusiveModeSrc(name string) string {
	return `import "std/i32";
function main(): i32 {
    // Idempotent: the working directory is shared between runs, so a file
    // left by an earlier one would make O_EXCL refuse this one and the test
    // would report a creation failure rather than the mode. Exclusivity
    // itself is pinned by open_exclusive_refuses_existing.
    match (remove_file("` + name + `")) { Ok(_) => {}, Err(_) => {} }
    match (open_exclusive("` + name + `")) {
        Ok(w) => {
            match (w.close()) { Some(_) => { return 91; }, None => {} }
        },
        Err(_) => { return 90; }
    }
    match (stat("` + name + `")) {
        Ok(st) => {
            // The assertion is that NOBODY ELSE can read it, not that the
            // mode is exactly 0600: a umask can only clear bits, never set
            // them, so "no group or other access" holds for a 0600 request
            // under every umask while a 0644 one fails it under all of them.
            // Exact equality would instead depend on the runner's umask —
            // this harness's is not the shell's, and a 0644 build reports
            // 0244 here against 0644 from a plain shell.
            var others: u32 = st.mode & (63 as u32);         // 0077
            if (others != 0 as u32) { return others as i32; }
            return 0;
        },
        Err(_) => { return 92; }
    }
    return 93;
}`
}

func TestX86_64OpenExclusiveMode(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, openExclusiveModeSrc("mode_probe_x86.txt")); code != 0 {
		t.Errorf("open_exclusive mode: got %d, want 0 (90=create, 91=close, 92=stat, else the group+other permission bits that leaked, in decimal)", code)
	}
}

func TestArm64OpenExclusiveMode(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, openExclusiveModeSrc("mode_probe_arm64.txt")); code != 0 {
		t.Errorf("open_exclusive mode: got %d, want 0 (90=create, 91=close, 92=stat, else the group+other permission bits that leaked, in decimal)", code)
	}
}

// Not wasm: preview-1's path_open and preview-2's open-at take no mode at
// all, so the file's permissions are the host's to choose and there is
// nothing here to assert.
