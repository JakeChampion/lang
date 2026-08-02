package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sortIRCases exercise the merge-sort SHAPE through the self-host IR path on
// x86-64 + wasm. The per-width monomorphic sort zoo (`sort_i32_asc` etc.)
// retired to core/cmp's generic `sort` / `sort_desc` (#5397), but the
// single-program driver resolves no imports, so the relevant sort surface is
// inlined verbatim as monomorphic i32 / string merge sorts matching the
// fresh-copy shape `cmp.sort` / `cmp.sort_desc` and std/sort's `sort_by`-based
// string sorts now ship: the i32 ascending / descending stable bottom-up merge
// sorts (direct scalar compares), the byte-lexicographic `string_cmp` three-way
// comparator, and the string ascending / descending merge sorts built on it.
// This verifies the constructs those sorts lower to compile on the IR path:
// scalar (`i32[]`) and pointer (`string[]`) array build via `.append` (the
// per-pass scratch buffer is materialized FRESH — NOT aliased + CoW, which the
// self-host AST/non-IR paths mutate through in place; see core/cmp's `sort` doc
// comment and the #5397 AST-corruption finding), element rewrite via `.with`,
// indexed scalar + string-byte reads, `.len()`, numeric `<`/`>` comparisons,
// the nested `while` merge loops, and the n < 2 return-borrowed-param early-out
// (executed by the i32-empty / string-singleton cases). Each program
// returns a small deterministic int (kept <= 126), pinned to the `"ir"`
// path; expectations are oracle-checked against the native interpreter.
// FEATURE-AUDIT std/sort + core/cmp sort row.
const sortIRPrelude = `pub function sort_i32_asc(arr: i32[]): i32[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: i32[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: i32[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (src[j] < src[i]) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function sort_i32_desc(arr: i32[]): i32[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: i32[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: i32[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (src[j] > src[i]) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function string_cmp(a: string, b: string): i32 {
    var n: i32 = a.len();
    var m: i32 = b.len();
    var min: i32 = n;
    if (m < n) { min = m; }
    var i: i32 = 0;
    while (i < min) {
        if (a[i] < b[i]) { return 0 - 1; }
        if (a[i] > b[i]) { return 1; }
        i = i + 1;
    }
    if (n < m) { return 0 - 1; }
    if (n > m) { return 1; }
    return 0;
}
pub function sort_strings_asc(arr: string[]): string[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: string[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: string[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (string_cmp(src[j], src[i]) < 0) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
pub function sort_strings_desc(arr: string[]): string[] {
    var n: i32 = arr.len();
    if (n < 2) { return arr; }
    var src: string[] = arr;
    var width: i32 = 1;
    while (width < n) {
        var dst: string[] = [];
        var c: i32 = 0;
        while (c < n) { dst = dst.append(src[c]); c = c + 1; }
        var lo: i32 = 0;
        while (lo < n) {
            var mid: i32 = lo + width;
            if (mid > n) { mid = n; }
            var hi: i32 = lo + width + width;
            if (hi > n) { hi = n; }
            var i: i32 = lo;
            var j: i32 = mid;
            var k: i32 = lo;
            while (i < mid && j < hi) {
                if (string_cmp(src[j], src[i]) > 0) {
                    dst = dst.with(k, src[j]);
                    j = j + 1;
                } else {
                    dst = dst.with(k, src[i]);
                    i = i + 1;
                }
                k = k + 1;
            }
            while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; }
            while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; }
            lo = lo + width + width;
        }
        src = dst;
        width = width + width;
    }
    return src;
}
function is_sorted_i32_asc(a: i32[]): i32 {
    var i: i32 = 1;
    while (i < a.len()) {
        if (a[i - 1] > a[i]) { return 0; }
        i = i + 1;
    }
    return 1;
}
`

var sortIRCases = []struct {
	name string
	main string
	want int
}{
	// ascending merge sort: [5,3,1,4,2] -> [1,2,3,4,5]; min*10 + max = 15.
	{"i32-asc", `var a: i32[] = [5, 3, 1, 4, 2]; var s: i32[] = sort_i32_asc(a); return s[0] * 10 + s[4];`, 15},
	// descending: [5,3,1,4,2] -> [5,4,3,2,1]; first*10 + last = 51.
	{"i32-desc", `var a: i32[] = [5, 3, 1, 4, 2]; var s: i32[] = sort_i32_desc(a); return s[0] * 10 + s[4];`, 51},
	// the ascending result is non-decreasing -> predicate returns 1.
	{"i32-sorted-pred", `var a: i32[] = [9, 1, 8, 2, 7, 3]; return is_sorted_i32_asc(sort_i32_asc(a));`, 1},
	// duplicates survive (stable count): [4,4,1,4] -> [1,4,4,4]; middle two are 4 -> 8.
	{"i32-dups", `var a: i32[] = [4, 4, 1, 4]; var s: i32[] = sort_i32_asc(a); return s[1] + s[2];`, 8},
	// byte-lex comparator: "apple" < "banana" -> -1; mapped to a small int via +2.
	{"string-cmp", `return string_cmp("apple", "banana") + 2;`, 1},
	// string sort ascending: smallest is "apple" (len 5).
	{"string-asc", `var a: string[] = ["cherry", "apple", "banana"]; var s: string[] = sort_strings_asc(a); return s[0].len();`, 5},
	// string sort descending: largest is "cherry" (len 6).
	{"string-desc", `var a: string[] = ["cherry", "apple", "banana"]; var s: string[] = sort_strings_desc(a); return s[0].len();`, 6},
	// n < 2 early-return actually EXECUTES (returns the borrowed scalar
	// array param directly): empty -> len 0 (+3 to distinguish from traps).
	{"i32-empty", `var e: i32[] = []; return sort_i32_asc(e).len() + 3;`, 3},
	// n < 2 early-return over a pointer-element (string[]) array: the
	// singleton survives the return-borrowed-param path intact.
	{"string-singleton", `var a: string[] = ["hi"]; var s: string[] = sort_strings_asc(a); return s[0].len();`, 2},
}

func sortIRSrc(mainBody string) string {
	return sortIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostSortIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, with the routing pinned to the "ir" path.
func TestSelfHostSortIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range sortIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(sortIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostSortIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostSortIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sort wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range sortIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(sortIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "sort_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("sort wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
