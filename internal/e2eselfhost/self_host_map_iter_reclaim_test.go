package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- A map cursor is freed on the typed lowering (#9562, self-host half) -----
//
// Every `m.iter()`, and so every `for (k, v) in m`, used to strand its cursor:
// the register backends built it with a bare `__fern_alloc` and the typed
// planner counted no unit of a MapIter. The cursor is now an rc-headed box the
// planner owns, released through `__fern_mapiter_free` (on wasm it also frees
// the key and value snapshots the cursor holds). Each shape runs 8 rounds, so
// a stranded cursor shows as live bytes on every backend.

const mapIterReclaimProlog = "import \"core/map\";\n" +
	"function drain(it: MapIter[string, i32]): i32 {\n" +
	"    var t: i32 = 0;\n" +
	"    while (it.has_next()) { t = t + it.value(); it.advance(); }\n" +
	"    return t;\n" +
	"}\n" +
	"function main(): i32 {\n" +
	"    var m: Map[string, i32] = map_new(4);\n" +
	"    m = m.insert(\"a\", 1);\n" +
	"    m = m.insert(\"bb\", 2);\n" +
	"    var total: i32 = 0;\n" +
	"    var i: i32 = 0;\n" +
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
		{"bound_cursor", "var it = m.iter(); while (it.has_next()) { total = total + it.value(); it.advance(); }", 24},
		{"fresh_argument", "total = total + drain(m.iter());", 24},
		{"aliased_argument", "var it = m.iter(); var alias = it; total = total + drain(alias);", 24},
		{"tuple_held", "var pair: (MapIter[string, i32], i32) = (m.iter(), 5); var (c, k) = pair; if (c.has_next()) { total = total + c.value() + k; }", 48},
		{"reassigned", "var it = m.iter(); it = m.iter(); if (it.has_next()) { total = total + it.value(); }", 8},
	}
}

func (c mapIterReclaimCase) src() string {
	return strings.Replace(mapIterReclaimProlog, "BODY", c.body, 1)
}

func TestSelfHostMapIterIsReclaimed(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapIterReclaimCases() {
		src := writeEnumMapSrc(t, "map_iter_"+tc.name, tc.src())
		t.Run(tc.name+"/x86-64", func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			assertMapIterRun(t, stderr, exit, tc.want)
		})
		t.Run(tc.name+"/arm64", func(t *testing.T) {
			armgcc, qemu := arm64Tooling(t)
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "map_iter_"+tc.name, string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			assertMapIterRun(t, eb.String(), cmd.ProcessState.ExitCode(), tc.want)
		})
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Skip("wasmtime not on PATH")
			}
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
			stderr, exit := runWasmCensus(t, wat)
			assertMapIterRun(t, stderr, exit, tc.want)
		})
	}
}

func assertMapIterRun(t *testing.T, stderr string, exit, want int) {
	t.Helper()
	if exit != want {
		t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
	}
	assertBalancedCensus(t, stderr)
}

// A module that iterates a cursor it did not make: the sibling's `drain` reads
// and advances one its caller made, which the caller releases.
func TestSelfHostMapIterAcrossModules(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	lib := "import \"core/map\";\npub function drain(it: MapIter[string, i32]): i32 {\n" +
		"    var t: i32 = 0;\n    while (it.has_next()) { t = t + it.value(); it.advance(); }\n    return t;\n}\n"
	main := "import \"core/map\";\nimport \"./lib\";\nfunction main(): i32 {\n" +
		"    var m: Map[string, i32] = map_new(4);\n    m = m.insert(\"a\", 1);\n    m = m.insert(\"bb\", 2);\n" +
		"    var total: i32 = 0;\n    var i: i32 = 0;\n    while (i < 8) { total = total + lib.drain(m.iter()); i = i + 1; }\n" +
		"    return total;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64", func(t *testing.T) {
		stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SEM_IR=1"), nil)
		assertMapIterRun(t, stderr, exit, 24)
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1"))
		assertMapIterRun(t, stderr, exit, 24)
	})
}
