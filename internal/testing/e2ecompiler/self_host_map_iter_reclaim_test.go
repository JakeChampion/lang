package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repeated snapshots must release their tuples and their elements.

const mapIterReclaimProlog = "import \"core/map\"; import \"core/iter\";\n" +
	"function drain(it: iter.ArrayIter[(string, i32)]): i32 {\n" +
	"    let t: i32 = 0;\n" +
	"    for (__mapkey_it, __mapvalue_it) in it { t = t + __mapvalue_it;  }\n" +
	"    return t;\n" +
	"}\n" +
	"function main(): i32 {\n" +
	"    let m: Map[string, i32] = map_new(4);\n" +
	"    m = m.insert(\"a\", 1);\n" +
	"    m = m.insert(\"bb\", 2);\n" +
	"    let total: i32 = 0;\n" +
	"    let i: i32 = 0;\n" +
	"    while (i < 8) { BODY i = i + 1; }\n" +
	"    return total % 256;\n" +
	"}\n"

type mapIterReclaimCase struct {
	name string
	body string
	want int
}

func mapIterReclaimCases() []mapIterReclaimCase {
	return []mapIterReclaimCase{
		{"for_in", "for (k, v) in m { total = total + v + k.len(); }", 48},
		{"for_in_break", "for (k, v) in m { if (v == 2) { break; } total = total + v; }", 8},
		{"bound_iterator", "let it = m.iter(); for (__mapkey_it, __mapvalue_it) in it { total = total + __mapvalue_it;  }", 24},
		{"fresh_argument", "total = total + drain(m.iter());", 24},
		{"aliased_argument", "let it = m.iter(); let alias = it; total = total + drain(alias);", 24},
		{"tuple_held", "let pair: (iter.ArrayIter[(string, i32)], i32) = (m.iter(), 5); let (c, k) = pair; if let Some(__mapstep_c) = c.next() { let __mapkey_c = __mapstep_c.0.0; let __mapvalue_c = __mapstep_c.0.1; total = total + __mapvalue_c + k; }", 48},
		{"reassigned", "let it = m.iter(); it = m.iter(); if let Some(__mapstep_it) = it.next() { let __mapkey_it = __mapstep_it.0.0; let __mapvalue_it = __mapstep_it.0.1; total = total + __mapvalue_it; }", 8},
	}
}

func (c mapIterReclaimCase) src() string {
	return strings.Replace(mapIterReclaimProlog, "BODY", c.body, 1)
}

func TestSelfHostMapIterIsReclaimed(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapIterReclaimCases() {
		runMapIterCensus(t, cli, "map_iter_"+tc.name, tc.name, tc.src(), tc.want)
	}
}

func runMapIterCensus(t *testing.T, cli *selfHostCLI, file, name, src string, want int) {
	path := writeEnumMapSrc(t, file, src)
	t.Run(name+"/x86-64", func(t *testing.T) {
		bin := cli.x86Binary(t, path, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", "FERN_STRICT_IR=1")
		stderr, exit := runWithStdin(t, cli.runner, bin, nil)
		assertMapIterRun(t, stderr, exit, want)
	})
	t.Run(name+"/arm64", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, path, "arm64-linux", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", "FERN_STRICT_IR=1"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), file, string(asm)))
		var eb strings.Builder
		cmd.Stderr = &eb
		_ = cmd.Run()
		assertMapIterRun(t, eb.String(), cmd.ProcessState.ExitCode(), want)
	})
	t.Run(name+"/wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		wat := cli.emit(t, path, "wasm32-wasi", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", "FERN_STRICT_IR=1")
		stderr, exit := runWasmCensus(t, wat)
		assertMapIterRun(t, stderr, exit, want)
	})
}

// Returned snapshots preserve their entries after the caller updates its Map.
const mapIterOutlivesFrameSrc = `import "core/map"; import "core/iter";
function over(m: Map[string, i32]): iter.ArrayIter[(string, i32)] { return m.iter(); }
function pair(m: Map[string, i32], n: i32): (iter.ArrayIter[(string, i32)], i32) { return (m.iter(), n); }
function many(m: Map[string, i32]): iter.ArrayIter[(string, i32)][] {
    let out: iter.ArrayIter[(string, i32)][] = [m.iter(), m.iter()];
    return out;
}
function pass(it: iter.ArrayIter[(string, i32)]): iter.ArrayIter[(string, i32)] { return it; }
function drain(it: iter.ArrayIter[(string, i32)]): i32 {
    let t: i32 = 0;
    for (__mapkey_it, __mapvalue_it) in it { t = t + __mapvalue_it + __mapkey_it.len();  }
    return t;
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 8) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        m = m.insert("kk", i + 1);
        let o = over(m);
        m = m.insert("kkk", 100);
        if (drain(o) != 2 * i + 4) { return 1; }
        let p = pair(m, i);
        total = total + drain(p.0) + p.1;
        let cs = many(m);
        total = total + drain(cs[0]) + drain(cs[1]);
        total = total + drain(pass(m.iter()));
        i = i + 1;
    }
    if (total != 3676) { return 2; }
    return 42;
}`

func TestSelfHostMapIterOutlivesItsFrame(t *testing.T) {
	runMapIterCensus(t, buildSelfHostCLI(t), "map_iter_outlives", "outlives", mapIterOutlivesFrameSrc, 42)
}

func assertMapIterRun(t *testing.T, stderr string, exit, want int) {
	t.Helper()
	if exit != want {
		t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostMapIterAcrossModules(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	lib := "import \"core/map\"; import \"core/iter\";\npub function drain(it: iter.ArrayIter[(string, i32)]): i32 {\n" +
		"    let t: i32 = 0;\n    for (__mapkey_it, __mapvalue_it) in it { t = t + __mapvalue_it;  }\n    return t;\n}\n"
	main := "import \"core/map\"; import \"core/iter\";\nimport \"./lib\";\nfunction main(): i32 {\n" +
		"    let m: Map[string, i32] = map_new(4);\n    m = m.insert(\"a\", 1);\n    m = m.insert(\"bb\", 2);\n" +
		"    let total: i32 = 0;\n    let i: i32 = 0;\n    while (i < 8) { total = total + lib.drain(m.iter()); i = i + 1; }\n" +
		"    return total;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64", func(t *testing.T) {
		stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", "FERN_STRICT_IR=1"), nil)
		assertMapIterRun(t, stderr, exit, 24)
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", "FERN_STRICT_IR=1"))
		assertMapIterRun(t, stderr, exit, 24)
	})
}
