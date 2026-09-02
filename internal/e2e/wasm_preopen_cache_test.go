package e2e

import (
	"strings"
	"testing"
)

// Every path-based preview-2 filesystem body resolves its path against a
// preopen descriptor from wasi:filesystem/preopens::get-directories, which
// MINTS a fresh own<descriptor> on each call. Nothing drops it, so looking
// it up per operation grew the host's resource table without bound — a
// program doing a million `stat`s did not merely slow down, it died with
// "resource table has no free keys" partway through, and peak RSS scaled
// with the call count (34 MB at 2k, 104 MB at 200k, 395 MB at the crash).
//
// The preopen set is fixed at instantiation, so the lookup is cached for the
// life of the process. A million operations is comfortably past where the
// table used to run out, and takes about two seconds once the lookup happens
// once.
func TestWASMFilesystemPreopenLookupIsCached(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
    var i: i32 = 0;
    var n: i32 = 0;
    while (i < 1000000) {
        match (stat("probe.txt")) { Ok(_) => { n = n + 1; }, Err(_) => {} }
        i = i + 1;
    }
    if (n != 1000000) { write("FAIL count " + n.to_string()); return 1; }
    write("preopen-cached");
    return 0;
}`
	stdout, stderr, _, _ := runWasmInDir(t, src, map[string]string{"probe.txt": "x"})
	if !strings.Contains(stdout, "preopen-cached") {
		t.Fatalf("stdout %q stderr %q; want preopen-cached", stdout, stderr)
	}
}
