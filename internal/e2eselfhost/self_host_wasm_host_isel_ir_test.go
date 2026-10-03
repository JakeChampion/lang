package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Three wasm programs the self-host refused or miscompiled where native
// answers them (#10769): `write_some` and `__store_u8` had no instruction
// selection, and `flags()` called a `$__fern_build_io_error` the module never
// defined, which watbin encoded as function index 2^32-1.
const wasmWriteSomeSrc = `import "std/i64";

function main(): i32 {
    match (stderr().write_some("ok\n")) {
        Err(_) => { return 1; },
        Ok(n) => { if (n != 3 as i64) { return 2; } }
    }
    match (stderr().write_some("")) {
        Err(_) => { return 3; },
        Ok(n) => { if (n != 0 as i64) { return 4; } }
    }
    return 0;
}
`

const wasmHandleFlagsSrc = `function main(): i32 {
    match (stdout().flags()) {
        Err(_) => { return 1; },
        Ok(f) => {
            if ((f & 2 as i64) == 0 as i64) { return 2; }
            if ((f & 4 as i64) != 0 as i64) { return 3; }
        }
    }
    match (stdin().flags()) {
        Err(_) => { return 4; },
        Ok(f) => { if ((f & 1 as i64) == 0 as i64) { return 5; } }
    }
    return 0;
}
`

// wasmStoreU8Src writes bytes into raw memory through both spellings of the
// byte store and reads them back.
const wasmStoreU8Src = `function main(): i32 {
    let p: usize = __alloc(8);
    __store_u8(p, 104);
    __store_u8(p + 1, 361);
    __raw_store8(p, 2, 106);
    if (__load_u8(p) != 104 || __load_u8(p + 1) != 105 || __raw_load8(p, 2) != 106) { return 1; }
    __free(p, 8);
    return 0;
}
`

// emitCoreModule compiles src to a preview-1 core module, the form watbin
// assembles, and returns its path.
func emitCoreModule(t *testing.T, c *strictCLI, src string) string {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "main.wasm")
	cmd := runX86_64Bin(c.runner, c.bin, "-target", "wasm32-wasi", "-emit", "core-module", mainPath, c.stdlib, "-o", out)
	cmd.Env = childEnv()
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	return out
}

func TestSelfHostWasmHostIselIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := newStrictCLI(t)
	for _, c := range []struct{ name, src, stderr string }{
		{"write-some", wasmWriteSomeSrc, "ok\n"},
		{"handle-flags", wasmHandleFlagsSrc, ""},
		{"store-u8", wasmStoreU8Src, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, emitCoreModule(t, cli, c.src))
			if exit != 0 || stderr != c.stderr {
				t.Fatalf("core module: exit %d, stderr %q; want 0, %q", exit, stderr, c.stderr)
			}
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", c.src)); code != 0 {
				t.Fatalf("WAT: exit %d, want 0", code)
			}
		})
	}
}

// TestSelfHostWatbinUndefinedReference pins watbin's refusal of a `$name`
// nothing defines, run as a program over watbin.fern itself.
func TestSelfHostWatbinUndefinedReference(t *testing.T) {
	cli := newStrictCLI(t)
	watbin, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, wat, stderr string }{
		{"defined", `(module (func $g) (func $f (call $g)))`, ""},
		{"undefined-call", `(module (func $f (call $g)))`, "watbin: reference to undefined $g\n"},
		{"undefined-global", `(module (func $f (result i32) (global.get $base)))`, "watbin: reference to undefined $base\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := string(watbin) + "\nfunction main(): i32 {\n    let b: i32[] = wat_to_binary(\"" + c.wat + "\");\n    return 0;\n}\n"
			bin := buildBin(t, cli.gcc, t.TempDir(), "watbin", cli.emit(t, "x86-64-linux", src))
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			want := 0
			if c.stderr != "" {
				want = 1
			}
			if exit != want || !strings.Contains(stderr, c.stderr) || (c.stderr == "" && stderr != "") {
				t.Fatalf("exit %d, stderr %q; want %d, %q", exit, stderr, want, c.stderr)
			}
		})
	}
}
