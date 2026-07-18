package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// aliasReassignReclaimCases pin the #3425 stage-2 alias-reassign reclaim fix: a
// FRESH struct local (`var t = S { arr: [] }`) that is later REASSIGNED from an
// ALIAS — a match-arm payload binding (`t = l`), a struct-field read (`t = x.s`),
// or an array-element read (`t = xs[i]`) — no longer owns a fresh box after the
// rebind; its rc fields BORROW whatever the RHS points into (still owned by its
// container). The self-host IR path used to keep such a local in
// reclaimable_names, so the loop's next-iteration `var t = ...` re-init reclaim
// (emit_field_reclaim_store) freed the container's live array out from under it
// — a double-free / use-after-free. This is exactly how the merged-bundle IR
// self-compile SIGSEGV'd (box_mutated_scalar_captures' `var lam = ExprLambda{};
// match (v.init) { ExprLambda(l) => { lam = l; } }` freed fd's own lambda body).
//
// reassigned_from_alias now excludes such locals from reclaim entirely — they
// leak, exactly like the borrowed match-payload struct bindings already do — so
// the container's arrays survive. The programs churn fresh arrays after scan()
// so the freed buffer is REUSED (7/8-filled) before the reads: a surviving
// double-free reads the reused data (sum wrong -> 90) rather than the originals.
var aliasReassignReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Array-element alias (`t = b.items[i]`): the ExprIndex reassign branch.
	{"alias-element", `struct S { arr: i32[] }
struct Box { items: S[] }
function scan(b: Box): Box {
    var i: i32 = 0;
    while (i < b.items.len()) {
        var t: S = S { arr: [] };
        t = b.items[i];
        var n: i32 = t.arr.len();
        i = i + 1;
    }
    return b;
}
function main(): i32 {
    var b: Box = Box { items: [S { arr: [1, 2, 3] }, S { arr: [4, 5, 6] }] };
    var b2: Box = scan(b);
    var j1: i32[] = [7, 7, 7];
    var j2: i32[] = [8, 8, 8];
    var sum: i32 = b2.items[0].arr[0] + b2.items[0].arr[1] + b2.items[0].arr[2] + b2.items[1].arr[0] + b2.items[1].arr[1] + b2.items[1].arr[2];
    if (sum != 21) { return 90; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Struct-field alias (`t = b.items[i].s`): the ExprFieldAccess reassign branch.
	{"alias-field", `struct S { arr: i32[] }
struct Wrap { s: S }
struct Box { items: Wrap[] }
function scan(b: Box): Box {
    var i: i32 = 0;
    while (i < b.items.len()) {
        var t: S = S { arr: [] };
        t = b.items[i].s;
        var n: i32 = t.arr.len();
        i = i + 1;
    }
    return b;
}
function main(): i32 {
    var b: Box = Box { items: [Wrap { s: S { arr: [1, 2, 3] } }, Wrap { s: S { arr: [4, 5, 6] } }] };
    var b2: Box = scan(b);
    var j1: i32[] = [7, 7, 7];
    var j2: i32[] = [8, 8, 8];
    var sum: i32 = b2.items[0].s.arr[0] + b2.items[1].s.arr[2];
    if (sum != 7) { return 90; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Negative: a fresh struct rebound from a fresh struct LITERAL (`t = S {..}`)
	// is NOT an alias — it stays reclaimable, values exact, no over-release.
	{"fresh-relit-safe", `struct S { arr: i32[] }
function main(): i32 {
    var t: S = S { arr: [0] };
    var i: i32 = 0;
    while (i < 100) { t = S { arr: [i, i + 1] }; i = i + 1; }
    var got: i32 = t.arr[0] + t.arr[1];
    if (got != 199) { return 90; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostAliasReassignReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (asm_run), which routes each small program through
// the IR path. Underflow-guarded; the corrupt-read manifests as exit 90.
func TestSelfHostAliasReassignReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range aliasReassignReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (90 = corrupt read via freed-then-reused buffer; 99 = over-release/underflow)", tc.name, code, tc.want)
			}
		})
	}
}
