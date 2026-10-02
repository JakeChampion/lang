package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostMapStructuralKeySource keys maps by tuples and arrays, compared and
// hashed element by element through helpers the lowering generates per key
// type (#10020). Every probe looks a key up through a value built separately
// from the one inserted, so a lowering that compared the key's box pointer
// answers the default. Covered: string, boolean, i64, u8 and char elements, a
// tuple nested in a tuple, an array of strings, an array of arrays, the empty
// array, overwrite, has / without, keys() and iteration, and enough entries to
// grow the columns.
const selfHostMapStructuralKeySource = `import "core/map";
import "std/i32";

function pair_name(i: i32): (string, i32) {
    return ("k" + i.to_string(), i % 7);
}

function yn(b: boolean): string {
    if (b) { return "t"; }
    return "f";
}

function main(): i32 {
    var a: Map[(string, i32), i32] = map_new(8);
    var i: i32 = 0;
    while (i < 300) {
        a = a.insert(pair_name(i), i);
        i = i + 1;
    }
    a = a.insert(("k5", 5), 500);
    var sa: i32 = a.get_or(("k" + 5.to_string(), 5), 0) + a.get_or(("k299", 299 % 7), 0) + a.get_or(("k1", 2), 0);

    var b: Map[(boolean, i64, (char, u8)), string] = map_new(4);
    b = b.insert((true, 9000000000i64, ('a', 2 as u8)), "x");
    b = b.insert((false, 9000000000i64, ('a', 2 as u8)), "y");
    var sb: string = b.get_or((true, 9000000000i64, ('a', 2 as u8)), "-") + b.get_or((false, 9000000000i64, ('a', 2 as u8)), "-")
        + b.get_or((true, 1i64, ('a', 2 as u8)), "-") + b.get_or((true, 9000000000i64, ('b', 2 as u8)), "-");

    var c: Map[string[], i32] = Map { ["ab", "c"]: 1, ["a", "bc"]: 2 };
    c = c.insert([], 3);
    var parts: string[] = ["a", "b" + "c"];
    var sc: i32 = c.get_or(parts, 0) * 100 + c.get_or(["ab", "c"], 0) * 10 + c.get_or([], 0);
    var hc: boolean = c.has(["ab"]);
    var (c2, had) = c.without(["ab", "c"]);

    var d: Map[i32[][], i32] = map_new(4);
    d = d.insert([[1, 2], [3]], 7);
    var sd: i32 = d.get_or([[1, 2], [3]], 0) + d.get_or([[1], [2, 3]], 0);

    var total: i32 = 0;
    for k in a.keys() {
        total = total + k.1;
    }
    var vs: i32 = 0;
    for (k, v) in c2 {
        vs = vs + v + k.len();
    }
    print("sa=" + sa.to_string() + " sb=" + sb + " sc=" + sc.to_string() + " hc=" + yn(hc)
        + " had=" + yn(had) + " after=" + c2.len().to_string()
        + " sd=" + sd.to_string() + " total=" + total.to_string() + " vs=" + vs.to_string() + " len=" + a.len().to_string());
    return 0;
}
`

// The interpreter's answer; the test checks the interpreter still gives it.
const selfHostMapStructuralKeyWant = "0|sa=799 sb=xy-- sc=213 hc=f had=t after=2 sd=7 total=897 vs=7 len=300\n"

// TestSelfHostMapStructuralKeys compiles the structural-key program for every
// self-host target and holds it to the interpreter's answer, with the
// sanitizer leg pinning that every key and value the columns own is released.
func TestSelfHostMapStructuralKeys(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostMapStructuralKeySource), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(buildLangBinForInterp(t), "-interp", src).Output()
	if err != nil || "0|"+string(out) != selfHostMapStructuralKeyWant {
		t.Fatalf("the interpreter answers %q (%v), want %q", out, err, strings.TrimPrefix(selfHostMapStructuralKeyWant, "0|"))
	}
	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, "")
			if got != selfHostMapStructuralKeyWant {
				t.Fatalf("answered %q, want %q\nreport: %s", got, selfHostMapStructuralKeyWant, report)
			}
			if leak != 0 {
				t.Fatalf("leaked %d bytes", leak)
			}
		})
	}
}
