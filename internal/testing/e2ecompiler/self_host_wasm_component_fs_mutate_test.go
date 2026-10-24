package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func buildFsMutateComponent(t *testing.T, cli *selfHostCLI, dir, src string) (string, string) {
	t.Helper()
	srcPath, comp := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, srcPath, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host component: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", comp).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	printed, err := exec.Command("wasm-tools", "print", comp).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, printed)
	}
	return comp, string(printed)
}

func requireWasmTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"wasmtime", "wasm-tools"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
}

func checkDeclares(t *testing.T, printed string, want, unwanted []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(printed, w) {
			t.Errorf("component does not declare %q", w)
		}
	}
	for _, u := range unwanted {
		if strings.Contains(printed, u) {
			t.Errorf("component declares %q, which its core never imports", u)
		}
	}
}

func TestSelfHostWasmComponentTempDirRemoveFile(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	comp, printed := buildFsMutateComponent(t, cli, dir, `function main(): i32 {
    let d: string = "";
    match (temp_dir("probe")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (write_file(d + "/a.txt", "hello")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file(d + "/a.txt")) {
        Err(e) => { return 1; },
        Ok(s) => { if (s != "hello") { return 1; } }
    }
    match (remove_file(d + "/a.txt")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file(d + "/a.txt")) { Ok(s) => { return 1; }, Err(e) => {} }
    match (remove_file(d + "/a.txt")) { Ok(_) => { return 1; }, Err(e) => {} }
    return 0;
}`)
	checkDeclares(t, printed,
		[]string{"[method]descriptor.create-directory-at", "[method]descriptor.unlink-file-at", "[method]descriptor.open-at"},
		[]string{"[method]descriptor.append-via-stream", "wasi:clocks/"})
	run := t.TempDir()
	if out, err := exec.Command("wasmtime", "run", "--dir", run, comp).CombinedOutput(); err != nil {
		t.Errorf("temp_dir + remove_file lifecycle: wasmtime run: %v\n%s", err, out)
	}
	ents, err := os.ReadDir(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || !ents[0].IsDir() || !regexp.MustCompile(`^probe-[0-9a-f]{8}$`).MatchString(ents[0].Name()) {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("preopen holds %q, want one probe-<8 hex> directory", names)
	}
}

func TestSelfHostWasmComponentStatOnly(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	comp, printed := buildFsMutateComponent(t, cli, dir, `function main(): i32 {
    match (stat("s.txt")) {
        Err(e) => { return 1; },
        Ok(fs) => {
            if (!fs.is_file) { return 1; }
            if (fs.is_dir) { return 1; }
            if (fs.size != 5) { return 1; }
        }
    }
    match (stat("sub")) {
        Err(e) => { return 1; },
        Ok(fs) => { if (fs.is_file) { return 1; } if (!fs.is_dir) { return 1; } }
    }
    match (stat("nope")) { Ok(fs) => { return 1; }, Err(e) => {} }
    return 0;
}`)
	checkDeclares(t, printed,
		[]string{"[method]descriptor.stat-at", `"descriptor-stat"`, `(record (field "seconds" u64) (field "nanoseconds" u32))`},
		[]string{"[method]descriptor.open-at", "wasi:clocks/wall-clock"})
	run := t.TempDir()
	if err := os.WriteFile(filepath.Join(run, "s.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(run, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("wasmtime", "run", "--dir", run, comp).CombinedOutput(); err != nil {
		t.Errorf("stat: wasmtime run: %v\n%s", err, out)
	}
}

func TestSelfHostWasmComponentCreateDirAll(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	comp, printed := buildFsMutateComponent(t, cli, dir, `function main(): i32 {
    match (create_dir_all("vendor/pkg/src")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (write_file("vendor/pkg/src/lib.fern", "hello")) { Err(e) => { return 1; }, Ok(_) => {} }
    match (read_file("vendor/pkg/src/lib.fern")) {
        Err(e) => { return 1; },
        Ok(s) => { if (s != "hello") { return 1; } }
    }
    match (create_dir_all("vendor/pkg/src")) { Err(e) => { return 1; }, Ok(_) => {} }
    return 0;
}`)
	checkDeclares(t, printed,
		[]string{"[method]descriptor.create-directory-at"},
		[]string{"[method]descriptor.unlink-file-at", "[method]descriptor.append-via-stream"})
	run := t.TempDir()
	if out, err := exec.Command("wasmtime", "run", "--dir", run, comp).CombinedOutput(); err != nil {
		t.Errorf("create_dir_all: wasmtime run: %v\n%s", err, out)
	}
	if fi, err := os.Stat(filepath.Join(run, "vendor", "pkg", "src")); err != nil || !fi.IsDir() {
		t.Errorf("vendor/pkg/src is not a directory under the preopen (err = %v)", err)
	}
}

func TestSelfHostWasmComponentDropCache(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	comp, printed := buildFsMutateComponent(t, cli, dir, `function main(): i32 {
    match (open_writer("cached.txt")) {
        Ok(w) => {
            match (w.write("cached\n")) { Ok(_) => {}, Err(_) => { return 1; } }
            match (w.drop_cache(0i64, 0i64)) { Ok(_) => {}, Err(_) => { return 1; } }
            w.close();
        },
        Err(_) => { return 1; }
    }
    match (open_reader("cached.txt")) {
        Ok(r) => {
            match (r.drop_cache(2i64, 0i64)) { Ok(_) => {}, Err(_) => { return 1; } }
            match (r.read_chunk(6)) { Ok(c) => { if (c != "cached") { return 1; } }, Err(_) => { return 1; } }
        },
        Err(_) => { return 1; }
    }
    return 0;
}`)
	checkDeclares(t, printed, []string{"[method]descriptor.advise"}, []string{"[method]descriptor.set-size"})
	run := t.TempDir()
	if out, err := exec.Command("wasmtime", "run", "--dir", run, comp).CombinedOutput(); err != nil {
		t.Errorf("drop_cache: wasmtime run: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(run, "cached.txt")); err != nil || string(got) != "cached\n" {
		t.Errorf("cached.txt = %q (err = %v), want %q", got, err, "cached\n")
	}
}
