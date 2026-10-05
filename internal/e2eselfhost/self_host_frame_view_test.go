package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A string view read only within the instruction that uses it keeps its box
// in the frame that made it (#8920). Each probe runs 100 rounds and prints its
// result times 1000 plus the heap allocations the rounds made. On the register
// backends scan and reads allocate nothing per round, copy_tail allocates only
// its copy, and wrapped still allocates a view per round because the view
// goes into a Some. Wasm's slice copies the bytes into a fresh block, so there
// every slice allocates.
const frameViewProgram = `@noinline function has_prefix(row: string, head: string): boolean {
    return row.len() > head.len() && slice_unchecked(row, 0, head.len()) == head;
}
@noinline function copy_tail(s: string): string { return slice_unchecked(s, 2, s.len()) + ""; }
@noinline function count_a(s: string): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < s.len()) { if (s[i] == b'a') { n = n + 1; } i = i + 1; }
    return n;
}
@noinline function reads(s: string): i32 {
    let mid: i32 = slice_unchecked(slice_unchecked(s, 1, s.len()), 1, 3).len();
    return mid + (slice_unchecked(s, 0, 2)[1] as i32) + count_a(slice_unchecked(s, 0, 4));
}
@noinline function wrapped(s: string): i32 {
    match (s[0:2]) {
        Some(v) => { return v.len(); },
        None => { return 0; }
    }
}
@noinline function scan(rows: string[], head: string): i32 {
    let n: i32 = 0;
    for r in rows { if (has_prefix(r, head)) { n = n + 1; } }
    return n;
}
@noinline function scan_rounds(rows: string[]): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + scan(rows, "beta("); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function copy_rounds(rows: string[]): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + copy_tail(rows[i % 3]).len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function read_rounds(rows: string[]): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + reads(rows[i % 3]); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function wrapped_rounds(rows: string[]): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + wrapped(rows[i % 3]); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    let rows: string[] = ["alpha(x)", "beta(y)", "banana(z)"];
    print_int(scan_rounds(rows)); print("");
    print_int(copy_rounds(rows)); print("");
    print_int(read_rounds(rows)); print("");
    print_int(wrapped_rounds(rows)); print("");
    return 0;
}
`

// Before frame views the register backends matched wasm's line for line.
const frameViewNative = "100000\n600100\n10539000\n200200\n"

func TestSelfHostFrameViews(t *testing.T) {
	runSemanticProgram(t, "frameview", frameViewProgram,
		[]string{"has_prefix", "copy_tail", "count_a", "reads", "wrapped", "scan", "scan_rounds", "copy_rounds", "read_rounds", "wrapped_rounds"},
		map[string]string{
			"arm64-linux":     frameViewNative,
			"x86-64-linux":    frameViewNative,
			"x86-64-sanitize": frameViewNative,
			"wasm32-wasi":     "100300\n600200\n10539400\n200200\n",
		})
}

// runSemanticProgram lowers every function of `program` but main through the
// semantic path, runs it on each target in `wants` (x86-64-sanitize is the
// x86-64 emit under FERN_SANITIZE), and checks its output starts with that
// target's want and its allocations balance.
func runSemanticProgram(t *testing.T, name, program string, produced []string, wants map[string]string) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "semsource_rc.fern"), []byte(semsourceRCDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	path := semsourceProgram(t, program)
	driver := buildSelfHostBin(t, gcc, dir, "semsource_rc.fern", "semsource-rc")
	for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
		want, ok := wants[target]
		if !ok {
			continue
		}
		t.Run(target, func(t *testing.T) {
			emitTarget, mode := target, "FERN_LEAKCHECK=1"
			if target == "x86-64-sanitize" {
				emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
			}
			cmd := runX86_64Bin(runner, driver, emitTarget, path)
			cmd.Env = append(os.Environ(), mode)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("semantic lowering: %v\n%s", err, diagnostics.String())
			}
			// A function spliced into every caller was produced there.
			for _, fn := range produced {
				if !strings.Contains(diagnostics.String(), "produced "+fn+"\n") && !strings.Contains(diagnostics.String(), "spliced "+fn+"\n") {
					t.Fatalf("%s was not produced:\n%s", fn, diagnostics.String())
				}
			}
			got, err := physicalRCRun(t, gcc, runner, dir, name, target, output).CombinedOutput()
			if err != nil {
				t.Fatalf("program: %v\n%s", err, got)
			}
			if !strings.HasPrefix(string(got), want) {
				t.Fatalf("program output:\n%s\nwant it to start:\n%s", got, want)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(got)), &allocs, &frees, &live); err != nil {
				t.Fatalf("%v\n%s", err, got)
			}
			if allocs != frees || live != 0 {
				t.Fatalf("unbalanced: %s", leakSummaryLine(string(got)))
			}
		})
	}
}
