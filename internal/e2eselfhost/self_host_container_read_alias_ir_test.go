package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// containerReadAliasCases pin the #3457 container-read alias-inc: an
// array-typed `var` binding whose init READS the buffer out of a container it
// does not own — a struct field (`var vn: string[] = h.names;`), a tuple
// element (`var xs: i32[] = t.0;`), or an array-of-array element
// (`var row: i32[] = g[i];`) — is a second reference to a buffer the container
// still points at. The self-host IR exit sweep decs every is_arr slot, so
// without a retain at the binding that dec frees the field out from under its
// owner: the buffer is released while the container still reads it, and the
// next allocation recycles it.
//
// Found by routing the self-host CHECKER through the IR path (the #3457
// AST-emitter retirement): `var vn: string[] = mod.enums[en].variant_names;`
// freed the enum table's variant-name buffer, after which unit-variant lookups
// read recycled memory — a bogus E001 on `return (A, 1)` and a missing E030 on
// a guarded-only match. The binding now takes the same Perceus dup the
// bare-ident alias (`var b = a;`) has always taken, so the sweep dec is
// balanced; a borrowed source leaks one count (sound), never over-frees.
var containerReadAliasCases = []struct {
	name string
	src  string
	want int
}{
	// Struct-field read: the checker's own shape. Each call to `total` binds
	// h.names into an annotated array local; the un-retained exit dec used to
	// free the literal's buffer on the FIRST call, so the loop's junk
	// allocations recycled it and later reads returned garbage.
	{"container-read-struct-field", `struct Holder { names: string[] }
function total(h: Holder): i32 {
    var vn: string[] = h.names;
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < vn.len()) { n = n + vn[i].len(); i = i + 1; }
    return n;
}
function main(): i32 {
    var h: Holder = Holder { names: ["ab", "cde"] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 200) {
        acc = (acc + total(h)) % 251;
        var junk: i32[] = [k, k + 1, k + 2];
        acc = (acc + junk[0]) % 251;
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (total(h) != 5) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Array-of-array element read and tuple-element read — the same alias, via
	// ExprIndex and a numeric ExprFieldAccess respectively.
	{"container-read-index-and-tuple", `function pick(g: i32[][], i: i32): i32 {
    var row: i32[] = g[i];
    return row[0] + row[1];
}
function firstof(t: (i32[], i32)): i32 {
    var xs: i32[] = t.0;
    return xs[0] + xs[1];
}
function main(): i32 {
    var g: i32[][] = [[1, 2], [3, 4]];
    var t: (i32[], i32) = ([5, 6], 7);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 200) {
        acc = (acc + pick(g, k % 2)) % 251;
        acc = (acc + firstof(t)) % 251;
        var junk: i32[] = [k, k + 1, k + 2];
        acc = (acc + junk[0]) % 251;
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (pick(g, 1) != 7) { return 98; }
    if (firstof(t) != 11) { return 97; }
    if (acc < 0) { return 96; }
    return 0;
}`, 0},
	// The REASSIGN sibling: `vn = h.names` over an already-array slot takes the
	// same alias, so lower_stmt_assign needs the matching retain (its
	// pre-existing field-read arm only covered scalar- / struct- / enum-element
	// fields, not string[], tuple elements, or array-of-array elements).
	{"container-read-reassign", `struct Holder { names: string[] }
function total(h: Holder, seed: string[]): i32 {
    var vn: string[] = seed;
    vn = h.names;
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < vn.len()) { n = n + vn[i].len(); i = i + 1; }
    return n;
}
function main(): i32 {
    var h: Holder = Holder { names: ["ab", "cde"] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 200) {
        acc = (acc + total(h, ["z"])) % 251;
        var junk: i32[] = [k, k + 1, k + 2];
        acc = (acc + junk[0]) % 251;
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (total(h, ["z"]) != 5) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostContainerReadAliasIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (asm_run), underflow-guarded.
func TestSelfHostContainerReadAliasIRX86_64(t *testing.T) {
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

	for _, tc := range containerReadAliasCases {
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
				t.Errorf("%s = %d, want %d (99 = over-release/underflow; 96-98 = container read corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
