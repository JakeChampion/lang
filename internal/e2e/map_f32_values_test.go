package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An f32 map value crosses into the map runtime's integer slot as its bit
// pattern and comes back out as an f32 (#10398). The SSA backends hold a float
// as an f64 and wasm types the operand, so each crossing needs its own
// conversion: insert, get, get_or's fallback and result, values(), a map
// literal, a string key's insert, and iteration. Before the fix the SSA
// backends read values() as zero and the wasm module failed to validate.
//
// The program prints rather than exits with its verdict because a WASI
// component reports every non-zero status as 1.
const mapF32ValuesSrc = `import "core/map";
import "std/i32";
function check(): i32 {
    var m: Map[i32, f32] = map_new(4);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(i, (i as f32) / 4.0);
        i = i + 1;
    }
    var vs: f32[] = m.values();
    if (vs.len() != 12) { return 1; }
    var vsum: f32 = 0.0 as f32;
    for v in vs { vsum = vsum + v; }
    if (vsum != 16.5 as f32) { return 2; }
    if (m.get_or(3, 9.0 as f32) != 0.75 as f32) { return 3; }
    if (m.get_or(99, 0.5 as f32) != 0.5 as f32) { return 4; }
    match (m.get(5)) {
        Some(v) => { if (v != 1.25 as f32) { return 5; } },
        None => { return 6; }
    }
    var lit: Map[i32, f32] = Map { 1: 2.5 as f32, 2: 0.25 as f32 };
    if (lit.get_or(1, 0.0 as f32) != 2.5 as f32) { return 7; }
    var lsum: f32 = 0.0 as f32;
    for (k, v) in lit { lsum = lsum + v; }
    if (lsum != 2.75 as f32) { return 8; }
    var named: Map[string, f32] = map_new(4);
    named = named.insert("a", 1.5 as f32);
    named = named.insert("b", -3.0 as f32);
    if (named.get_or("b", 0.0 as f32) != -3.0 as f32) { return 9; }
    var nsum: f32 = 0.0 as f32;
    for (k, v) in named { nsum = nsum + v; }
    if (nsum != -1.5 as f32) { return 10; }
    return 0;
}
function main(): i32 {
    stdout().write("check=" + check().to_string() + "\n");
    return 0;
}`

func TestMapF32ValuesOnEveryBackend(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(mapF32ValuesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	wasmtime, wasmErr := exec.LookPath("wasmtime")
	for _, c := range []struct {
		name, target, backend string
		runnable              bool
		run                   func(bin string) *exec.Cmd
	}{
		{"x86-64", "x86-64-linux", "", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"x86-64-ssa", "x86-64-linux", "ssa", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"arm64", "arm64-linux", "", armok, func(bin string) *exec.Cmd { return runX86Bin(arm, bin) }},
		{"arm64-ssa", "arm64-linux", "ssa", armok, func(bin string) *exec.Cmd { return runX86Bin(arm, bin) }},
		{"wasm", "wasm32-wasi", "", wasmErr == nil, func(bin string) *exec.Cmd { return exec.Command(wasmtime, bin) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.runnable {
				t.Fatalf("no way to run %s binaries on this host", c.target)
			}
			bin := filepath.Join(t.TempDir(), "prog")
			args := []string{"-target", c.target}
			if c.backend != "" {
				args = append(args, "-backend", c.backend)
			}
			args = append(args, "-o", bin, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := c.run(bin).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "check=0" {
				t.Fatalf("got %q (%v), want check=0", out, err)
			}
		})
	}
}
