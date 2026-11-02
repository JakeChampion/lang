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

const (
	dirDeepLevels = 60
	dirDeepWidth  = 100
)

func dirHandleSource(root string, deep bool) string {
	walk := ""
	if deep {
		walk = fmt.Sprintf(`
    if (rm_tree(top, "deep") != %d) { return 42; }`, dirDeepLevels+1)
	}
	return fmt.Sprintf(`// rm_tree removes the directory `+"`name`"+` inside d, answering how many
// directories deep the longest chain under it ran, or a negative step code.
function rm_tree(d: Dir, name: string): i32 {
    let Ok(c) = d.open_dir(name) else { return 0 - 1000; };
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

// open_under opens files under top and sub by name. The step codes run from
// 50. The directory sub is renamed away and replaced while its handle is open,
// and the handle must still name the original.
function open_under(top: Dir, sub: Dir, root: string): i32 {
    let Ok(r) = top.open_reader("f") else { return 50; };
    match (r.read_chunk(16)) {
        Ok(s) => { if (s != "hello") { return 51; } },
        Err(_) => { return 52; }
    }
    match (r.close()) { Ok(_) => {}, Err(_) => { return 53; } }
    match (top.open_reader("l")) { Ok(_) => { return 54; }, Err(_) => {} }
    match (create_symlink("f", root + "/lf")) { Ok(_) => {}, Err(_) => { return 55; } }
    match (top.open_reader("lf")) { Ok(_) => { return 56; }, Err(e) => { match (e) { Other(p, _, n) => { if (p != "lf" || n != 40) { return 57; } }, _ => { return 58; } } } }
    match (top.open_reader_with("lf", 0)) { Ok(_) => { return 59; }, Err(_) => {} }
    match (top.remove_file("lf")) { Ok(_) => {}, Err(_) => { return 60; } }
    match (top.open_reader("nope")) {
        Ok(_) => { return 61; },
        Err(e) => { match (e) { NotFound(p) => { if (p != "nope") { return 62; } }, _ => { return 63; } } }
    }
    let Ok(w) = top.open_writer("w") else { return 64; };
    match (w.write("abc")) { Ok(_) => {}, Err(_) => { return 65; } }
    match (w.close()) { Ok(_) => {}, Err(_) => { return 66; } }
    match (top.lstat("w")) { Ok(st) => { if (st.size != 3i64) { return 67; } }, Err(_) => { return 68; } }
    let Ok(w2) = top.open_writer("w") else { return 69; };
    match (w2.write("z")) { Ok(_) => {}, Err(_) => { return 70; } }
    match (w2.close()) { Ok(_) => {}, Err(_) => { return 71; } }
    match (top.lstat("w")) { Ok(st) => { if (st.size != 1i64) { return 72; } }, Err(_) => { return 73; } }
    match (top.open_writer_with("w", 1 | 4)) { Ok(_) => { return 74; }, Err(e) => { match (e) { AlreadyExists(p) => { if (p != "w") { return 75; } }, _ => { return 76; } } } }
    let Ok(w3) = top.open_writer_with("w", 0) else { return 77; };
    match (w3.write("yy")) { Ok(_) => {}, Err(_) => { return 78; } }
    match (w3.close()) { Ok(_) => {}, Err(_) => { return 79; } }
    match (top.lstat("w")) { Ok(st) => { if (st.size != 2i64) { return 80; } }, Err(_) => { return 81; } }
    match (top.remove_file("w")) { Ok(_) => {}, Err(_) => { return 82; } }
    match (rename(root + "/sub", root + "/swap")) { Ok(_) => {}, Err(_) => { return 83; } }
    match (create_dir(root + "/sub", 448)) { Ok(_) => {}, Err(_) => { return 84; } }
    match (write_file(root + "/sub/g", "evil")) { Ok(_) => {}, Err(_) => { return 85; } }
    let Ok(g) = sub.open_reader("g") else { return 86; };
    match (g.read_chunk(16)) {
        Ok(s) => { if (s != "x") { return 87; } },
        Err(_) => { return 88; }
    }
    match (g.close()) { Ok(_) => {}, Err(_) => { return 89; } }
    match (remove_file(root + "/sub/g")) { Ok(_) => {}, Err(_) => { return 90; } }
    match (remove_dir(root + "/sub")) { Ok(_) => {}, Err(_) => { return 91; } }
    match (rename(root + "/swap", root + "/sub")) { Ok(_) => {}, Err(_) => { return 92; } }
    return 0;
}

function main(): i32 {
    let Ok(top) = open_dir(%[1]q) else { return 10; };
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
    let Ok(sub) = top.open_dir("sub") else { return 23; };
    match (sub.entries()) {
        Ok(names) => { if (names.len() != 1) { return 24; } if (names[0] != "g") { return 25; } },
        Err(_) => { return 26; }
    }
    let k: i32 = open_under(top, sub, %[1]q);
    if (k != 0) { return k; }
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
    match (sub.close()) { Ok(_) => {}, Err(_) => { return 39; } }
    match (top.remove_dir("sub")) { Ok(_) => {}, Err(_) => { return 40; } }
    match (open_dir(%[2]q)) { Ok(_) => { return 41; }, Err(_) => {} }%[3]s
    unsafe { match (top.close_unchecked()) { Ok(_) => {}, Err(_) => { return 43; } } }
    match (top.close()) { Ok(_) => { return 44; }, Err(_) => {} }
    return 0;
}
`, root, filepath.Join(root, "f"), walk)
}

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

func TestArm64DarwinDirHandle(t *testing.T) {
	dir := t.TempDir()
	root := dirHandleFixture(t, dir, true)
	if !buildAndRunDarwin(t, dir, dirHandleSource(root, true)) {
		return
	}
	dirHandleCheckLeft(t, root)
}

func TestInterpDirHandle(t *testing.T) {
	root := dirHandleFixture(t, t.TempDir(), false)
	if code := runInterpExit(t, dirHandleSource(root, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirHandleSource)", code)
	}
	dirHandleCheckLeft(t, root)
}

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
		sawDir := false
		for _, v := range vs {
			switch {
			case v.Builtin == "open_dir":
				sawDir = true
			case target == "wasm32-wasi-http" && v.Capability == "fs":
				// The proxy target provides no filesystem at all, so the
				// swap's rename and create_dir are refused with it.
			default:
				t.Errorf("%s: unexpected violation %+v", target, v)
			}
		}
		if !sawDir {
			t.Errorf("%s: no violation names open_dir, want it refused", target)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}

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
