package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A narrow integer or boolean map key crosses into core/map's pointer-wide
// slot, which core/map compares at full width. On a 64-bit target the slot's
// upper half has to be the widening `as usize` gives, whichever instruction
// produced the key; otherwise a key computed in a loop and the equal literal
// are different slots (#10442). Every op that takes a key or a value is
// crossed both ways here: keys computed in a loop and read by literal, and
// literal keys read by computed ones.
//
// The program prints rather than exits with its verdict because a WASI
// component reports every non-zero status as 1.
const mapNarrowKeysSrc = `import "core/map";
import "std/i32";
function neg(i: i32): i32 { return 0 - i; }
function check(): i32 {
    let m: Map[i32, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 40) { m = m.insert(i - 20, (i - 20) * 3); i = i + 1; }
    if (m.get_or(-20, 7777) != -60) { return 1; }
    match (m.get(-19)) {
        Some(v) => { if (v != -57) { return 2; } },
        None => { return 3; }
    }
    if (!m.has(-1)) { return 4; }
    m = m.insert(-20, 5);
    if (m.len() != 40) { return 5; }
    let r: (Map[i32, i32], boolean) = m.without(-18);
    if (!r.1) { return 6; }
    m = r.0;
    if (m.has(-18) || m.len() != 39) { return 7; }
    let lit: Map[i32, i32] = Map { -3: 30, -4: 40 };
    let j: i32 = 0;
    let s: i32 = 0;
    while (j < 5) { s = s + lit.get_or(neg(j), 0); j = j + 1; }
    if (s != 70) { return 8; }
    let ks: i32 = 0;
    for k in m.keys() { ks = ks + k; }
    if (ks != -2) { return 9; }
    let us: Map[u32, i32] = map_new(4);
    let u: u32 = 3000000000 as u32;
    let ui: i32 = 0;
    while (ui < 3) { us = us.insert(u + (ui as u32), ui); ui = ui + 1; }
    if (us.get_or(3000000001 as u32, 9) != 1) { return 10; }
    let flags: Map[boolean, i32] = map_new(4);
    let f: i32 = 0;
    while (f < 4) { flags = flags.insert(f % 2 == 1, f); f = f + 1; }
    if (flags.len() != 2 || flags.get_or(true, 0) != 3 || flags.get_or(false, 0) != 2) { return 11; }
    return 0;
}
function main(): i32 {
    stdout().write("check=" + check().to_string() + "\n");
    return 0;
}`

func TestMapNarrowKeysOnEveryBackend(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(mapNarrowKeysSrc), 0o644); err != nil {
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
				t.Skipf("no way to run %s binaries on this host", c.target)
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
