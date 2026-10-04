package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The self-host twins of internal/e2e/wasm_component_fs_mutate_test.go: the
// path-mutating filesystem builtins in a component the self-host CLI builds,
// and the instance types that component declares. A component that declares a
// method its core never imports demands a capability it has no use for, so
// each test pins what wasi:filesystem/types declares as well as what runs.
// main returns 0 only when every step passed, since a component reports only
// 0 or non-zero.

// buildFsMutateComponent compiles src to a component with the self-host CLI
// and returns its path and its `wasm-tools print` text.
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

// checkDeclares fails for each of want the component does not declare and
// each of unwanted it does.
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

// TestSelfHostWasmComponentTempDirRemoveFile runs create, write, read, unlink
// and check-gone in a component. Reading after remove_file must fail, or
// unlink-file-at never ran; removing what is gone must fail too. The directory
// temp_dir made is `probe-` and eight hex digits under the preopen, the name
// native's component gives it.
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

// TestSelfHostWasmComponentStatOnly runs stat and nothing else. stat-at takes
// the preopen and a path, so the component must not declare open-at. It must
// declare the whole descriptor-stat record, timestamps included, but with
// `datetime` declared inline, so it imports no clock it never reads.
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

// TestSelfHostWasmComponentCreateDirAll creates a nested path, writes into its
// leaf, reads it back and creates it again, which must be Ok. A program that
// only creates directories declares create-directory-at without
// unlink-file-at.
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
