package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// enumPayloadArrReclaimCases pin #6758's recursive half: an enum variant whose
// payload is an array OF THAT ENUM — the shape every tree type is built from.
//
//	enum N { Leaf(i32), Seq(N[]) }
//
// `enum_field_rc_droppable` reached `is_leaksafe_array_field`, which admits only
// scalar-element arrays, so `N[]` made `Seq` non-droppable — and
// `enum_all_variants_rc_droppable` bails on the first non-droppable variant, so
// the whole enum was refused. That took the *scalar* `Leaf` variant down with
// it: `let n: N = Leaf(i)` leaked 40 B/iteration while the byte-identical
// `let m: M = A(i)` on a non-recursive enum was flat.
//
// Measured on x86-64, `__heap_bump_bytes()` delta across two identical churn
// calls, bytes per iteration (native is 0 on all three):
//
//	let n: N = Leaf(i);                              40  -> 0
//	let n: N = Seq([Leaf(i)]);                      112  -> 0
//	let kids: N[] = [Leaf(i), Leaf(8)]; Seq(kids);  160  -> 0
//
// The release is `__fern_arrarr_free`: it rc-guards the buffer, decs each
// element BOX (rc-guarded, so a shared element only decs), then frees the
// buffer. Reusing that runtime helper is what kept this to one file — it is
// already need-registered by name in all three backends' call scans, unlike the
// `__struct_arr_elems_drop_<E>` helper, whose need is recorded only inside the
// struct-drop generator.
//
// DEPTH IS ONE LEVEL, deliberately. An element's own payload survives one level
// down — `Seq([Seq([…])])` still strands the inner buffer — which is the same
// model the struct-array path documents, and it leaks rather than dangles. The
// nested case is not asserted flat below for that reason; it is asserted
// CORRECT, since a wrong free there would be a use-after-free rather than a
// byte count.
var enumPayloadArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The scalar variant of a recursive enum. Nothing about this value involves
	// an array — it leaked only because a sibling variant did.
	{"recursive-enum-scalar-variant-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let n: N = Leaf(i); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let n2: N = Leaf(j); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The array-payload variant: buffer plus element boxes, all per iteration.
	{"recursive-enum-array-payload-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let n: N = Seq([Leaf(i), Leaf(8)]); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let n2: N = Seq([Leaf(j), Leaf(8)]); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The payload built in a LOCAL first, which is #6758's third row verbatim.
	{"recursive-enum-payload-via-local-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let kids: N[] = [Leaf(i), Leaf(8)]; let n: N = Seq(kids); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let k2: N[] = [Leaf(j), Leaf(8)]; let n2: N = Seq(k2); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE guard. The payload is read back through a match after the loop has
	// run many times, so an element freed while still reachable is a wrong
	// answer (97) or a crash, not a byte count. This is the assertion that
	// matters most: the release walks a buffer of live boxes.
	{"recursive-enum-values-readable", `enum N { Leaf(i32), Seq(N[]) }
function first_leaf(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(kids) => { return kids.len(); }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let n: N = Seq([Leaf(i), Leaf(8), Leaf(9)]);
        if (first_leaf(n) != 3) { return 97; }
        let m: N = Leaf(i);
        if (first_leaf(m) != i) { return 97; }
        acc = (acc + 1) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 96; }
    return 0;
}`, 0},
	// NESTED, two levels. Not asserted flat — the one-level release strands the
	// inner buffer — but asserted CORRECT and over-release-free, which is what
	// separates "leaks a level" from "frees something still live".
	{"recursive-enum-nested-correct", `enum N { Leaf(i32), Seq(N[]) }
function depth_sum(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(kids) => {
            let s: i32 = 0;
            let i: i32 = 0;
            while (i < kids.len()) { s = s + depth_sum(kids[i]); i = i + 1; }
            return s;
        }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let inner: N = Seq([Leaf(1), Leaf(2)]);
        let outer: N = Seq([inner, Leaf(3)]);
        if (depth_sum(outer) != 6) { return 97; }
        acc = (acc + 1) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 96; }
    return 0;
}`, 0},
}

// TestSelfHostEnumPayloadArrReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler, heap-bump + underflow + value guarded.
func TestSelfHostEnumPayloadArrReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range enumPayloadArrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = payload still per-iteration; 99 = over-release/underflow; 96-97 = value corrupted)",
					tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostEnumPayloadArrReclaimIRArm64 is the arm64 leg — the other backend
// that boxes an enum payload, and the one where a wrong release shows as a
// SIGSEGV under qemu rather than a byte count.
func TestSelfHostEnumPayloadArrReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range enumPayloadArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
