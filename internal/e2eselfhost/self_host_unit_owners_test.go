package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPerModuleUnitOwners pins that the asm and wasm per-module drivers
// cut the typed lowering into the same units: both call ircore.split_units, so
// -per-module-func-counts agrees line for line. The leaf declares a method on
// `str`, which the lowering spells `string`; only main calls it, so matching
// the declaration on the unerased spelling would hand it to the entry.
func TestSelfHostPerModuleUnitOwners(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_modload_run.fern")
	copySelfHostDriver(t, dir, "wasm_modload_run.fern")
	asmBin := cachedDriverBin(t, gcc, dir, "asm_modload_run.fern")
	wasmBin := cachedDriverBin(t, gcc, dir, "wasm_modload_run.fern")

	proj := t.TempDir()
	for name, src := range map[string]string{
		"leaf.fern": `pub trait Shout {
    function shout(self: Self): i32;
}

impl Shout for str {
    function shout(self: Self): i32 { return self.len() + 1; }
}

pub function pick[T](a: T, b: T, first: boolean): T {
    if (first) { return a; }
    return b;
}

pub function scaled(k: i32): i32 {
    var f = (x: i32): i32 => x * k;
    return f(3);
}
`,
		"main.fern": `import "./leaf";

function main(): i32 {
    var s: str = "ab";
    return s.shout() + leaf.pick(1, 2, true) + leaf.scaled(2);
}
`,
	} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(proj, "main.fern")
	counts := func(bin string) string {
		t.Helper()
		cmd := runX86_64Bin(runner, bin, entry, "-per-module-func-counts")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s -per-module-func-counts: %v\n%s", filepath.Base(bin), err, out)
		}
		return strings.Join(strings.Fields(string(out)), " ")
	}
	// leaf: shout, pick, scaled and its lifted closure; the entry: main.
	const want = "4 1"
	for _, bin := range []string{asmBin, wasmBin} {
		if got := counts(bin); got != want {
			t.Errorf("%s units %q, want %q", filepath.Base(bin), got, want)
		}
	}
}
