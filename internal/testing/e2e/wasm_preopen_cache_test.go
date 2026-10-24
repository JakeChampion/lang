package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWASMFilesystemPreopenLookupIsCached(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
    let i: i32 = 0;
    let n: i32 = 0;
    while (i < 1000000) {
        match (stat("probe.txt")) { Ok(_) => { n = n + 1; }, Err(_) => {} }
        i = i + 1;
    }
    if (n != 1000000) { write("FAIL count " + n.to_string()); return 1; }
    write("preopen-cached");
    return 0;
}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, _ := runCLIComponent(t, src, runOpts{workDir: dir})
	if !strings.Contains(stdout, "preopen-cached") {
		t.Fatalf("stdout %q stderr %q; want preopen-cached", stdout, stderr)
	}
}
