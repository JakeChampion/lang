// The directory handle end to end: `open_dir` and every Dir method against a
// fixture — a file, a subdirectory, a symlink to it — and, on the compiled
// targets, a walk that removes a tree deeper than PATH_MAX by descending one
// handle at a time. A path-taking walk stops at 4096 bytes on Linux and 1024
// on Darwin; this one never forms a path longer than a component.
//
// Each exit code names the step that failed; 0 is a pass.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// dirDeepLevels directories of dirDeepWidth bytes each are about 6 KiB of
// path, past both kernels' PATH_MAX.
const (
	dirDeepLevels = 60
	dirDeepWidth  = 100
)

// dirHandleSource is the probe, rooted at `root`. `deep` adds the walk that
// removes ./deep, which must report dirDeepLevels+1 directories.
func dirHandleSource(root string, deep bool) string {
	walk := ""
	if deep {
		walk = fmt.Sprintf(`
    if (rm_tree(top, "deep") != %d) { return 42; }`, dirDeepLevels+1)
	}
	return fmt.Sprintf(`// rm_tree removes the directory `+"`name`"+` inside d, answering how many
// directories deep the longest chain under it ran, or a negative step code.
function rm_tree(d: Dir, name: string): i32 {
    let c: Dir = match (d.open_dir(name)) { Ok(c) => c, Err(_) => { return 0 - 1000; } };
    let n: i32 = 0;
    match (c.entries()) {
        Ok(names) => {
            for x in names {
                match (c.lstat(x)) {
                    Ok(st) => {
                        if (st.is_dir) {
                            let k: i32 = rm_tree(c, x);
                            if (k < 0) { return k; }
                            if (k > n) { n = k; }
                        } else {
                            match (c.remove_file(x)) { Ok(_) => {}, Err(_) => { return 0 - 2000; } }
                        }
                    },
                    Err(_) => { return 0 - 3000; }
                }
            }
        },
        Err(_) => { return 0 - 4000; }
    }
    c.close();
    match (d.remove_dir(name)) { Ok(_) => {}, Err(_) => { return 0 - 5000; } }
    return n + 1;
}

function main(): i32 {
    let top: Dir = match (open_dir(%[1]q)) { Ok(d) => d, Err(_) => { return 10; } };
    match (top.lstat("f")) {
        Ok(st) => { if (!st.is_file) { return 11; } if (st.size != 5i64) { return 12; } },
        Err(_) => { return 13; }
    }
    match (top.lstat("l")) {
        Ok(st) => { if (st.is_dir || st.is_file) { return 14; } },
        Err(_) => { return 15; }
    }
    match (top.stat("l")) {
        Ok(st) => { if (!st.is_dir) { return 16; } },
        Err(_) => { return 17; }
    }
    match (top.open_dir("l")) { Ok(_) => { return 18; }, Err(_) => {} }
    match (top.open_dir("f")) { Ok(_) => { return 19; }, Err(_) => {} }
    match (top.lstat("nope")) {
        Ok(_) => { return 20; },
        Err(e) => { match (e) { NotFound(p) => { if (p != "nope") { return 21; } }, _ => { return 22; } } }
    }
    let sub: Dir = match (top.open_dir("sub")) { Ok(d) => d, Err(_) => { return 23; } };
    match (sub.entries()) {
        Ok(names) => { if (names.len() != 1) { return 24; } if (names[0] != "g") { return 25; } },
        Err(_) => { return 26; }
    }
    match (sub.entries()) {
        Ok(names) => { if (names.len() != 1) { return 27; } },
        Err(_) => { return 28; }
    }
    match (sub.access("g", 4)) { Ok(_) => {}, Err(_) => { return 29; } }
    match (sub.access("nope", 0)) { Ok(_) => { return 30; }, Err(_) => {} }
    match (sub.chmod("g", 384, true)) { Ok(_) => {}, Err(_) => { return 31; } }
    match (sub.lstat("g")) {
        Ok(st) => { if ((st.mode & 4095u32) != 384u32) { return 32; } },
        Err(_) => { return 33; }
    }
    match (sub.chown("g", 0 - 1, 0 - 1, false)) { Ok(_) => {}, Err(_) => { return 34; } }
    match (top.remove_dir("sub")) { Ok(_) => { return 35; }, Err(_) => {} }
    match (sub.remove_dir("g")) { Ok(_) => { return 36; }, Err(_) => {} }
    match (sub.remove_file("g")) { Ok(_) => {}, Err(_) => { return 37; } }
    match (sub.remove_file("g")) { Ok(_) => { return 38; }, Err(_) => {} }
    match (sub.close()) { None => {}, Some(_) => { return 39; } }
    match (top.remove_dir("sub")) { Ok(_) => {}, Err(_) => { return 40; } }
    match (open_dir(%[2]q)) { Ok(_) => { return 41; }, Err(_) => {} }%[3]s
    match (top.close()) { None => {}, Some(_) => { return 43; } }
    match (top.close()) { None => { return 44; }, Some(_) => {} }
    return 0;
}
`, root, filepath.Join(root, "f"), walk)
}

// dirHandleFixture builds the probe's tree in a fresh directory under dir
// and answers its path. The deep chain is made by a shell stepping into each
// directory it creates; `cd -P` keeps the shell from joining it onto $PWD,
// so no path to it is ever formed.
func dirHandleFixture(t *testing.T, dir string, deep bool) string {
	t.Helper()
	root := filepath.Join(dir, "work")
	script := `set -e
mkdir work && cd work
printf hello > f
mkdir sub && printf x > sub/g
ln -s sub l
`
	if deep {
		script += fmt.Sprintf(`mkdir deep && cd deep
i=0
while [ $i -lt %d ]; do mkdir %[2]s && cd -P %[2]s; i=$((i + 1)); done
printf leaf > leaf
`, dirDeepLevels, strings.Repeat("d", dirDeepWidth))
	}
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	return root
}

// dirHandleCheckLeft wants the probe to have left only the file and the link.
func dirHandleCheckLeft(t *testing.T, root string) {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "f l" {
		t.Errorf("left behind %q, want \"f l\"", got)
	}
}

func TestX86_64DirHandle(t *testing.T) {
	code, dir := compileRunX86_64WithSetup(t, dirHandleSource("work", true), func(dir string) {
		dirHandleFixture(t, dir, true)
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirHandleSource)", code)
	}
	dirHandleCheckLeft(t, filepath.Join(dir, "work"))
}

func TestArm64DirHandle(t *testing.T) {
	root := dirHandleFixture(t, t.TempDir(), true)
	if _, code := compileAndRunArm64(t, dirHandleSource(root, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirHandleSource)", code)
	}
	dirHandleCheckLeft(t, root)
}

// XNU's *at calls take Linux's shapes under their own numbers and flag bits;
// built everywhere, run on Apple Silicon.
func TestArm64DarwinDirHandle(t *testing.T) {
	dir := t.TempDir()
	root := dirHandleFixture(t, dir, true)
	if !buildAndRunDarwin(t, dir, dirHandleSource(root, true)) {
		return
	}
	dirHandleCheckLeft(t, root)
}

// The interpreter resolves a name by the Dir's path, so it is held to
// PATH_MAX and runs the probe without the deep walk.
func TestInterpDirHandle(t *testing.T) {
	root := dirHandleFixture(t, t.TempDir(), false)
	if code := runInterpExit(t, dirHandleSource(root, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirHandleSource)", code)
	}
	dirHandleCheckLeft(t, root)
}

// No wasm backend lowers the handle, so `open_dir` is gated on `fsdir`
// rather than answering at run time; the methods need no gate of their own.
func TestDirHandleGatedOnFsdir(t *testing.T) {
	prog, err := parser.Parse(dirHandleSource("/tmp", false))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s: no violations, want open_dir refused", target)
		}
		for _, v := range vs {
			if v.Builtin != "open_dir" {
				t.Errorf("%s: unexpected violation %+v", target, v)
			}
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}

// The compiler's own gate (compiler/platforms.fern) refuses it too.
func TestDirHandleRefusedOnWasm(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(dirHandleSource("/tmp", false)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "probe.wasm"), src).CombinedOutput()
	if err == nil {
		t.Fatal("wasm compiled a program that opens a Dir")
	}
	if !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "open_dir") {
		t.Fatalf("refusal should be E066 naming open_dir, got:\n%s", out)
	}
}
