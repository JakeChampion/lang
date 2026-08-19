package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The receiver-borrow deep drop (#6544).
//
// `moves_fields_expr` marks EVERY method receiver as a field-move hazard, so
// `b.score()` costs `b` its "NODEEP:" credit and the exit sweep degrades to a
// box-only dec — the struct's own string / array fields are stranded for the
// rest of the scope. A method body genuinely can carry a receiver field into
// its result uncounted (`ops: self.ops.append(op)`, the builder shape), which
// is why the mark exists; most methods cannot, and each one that cannot was
// paying 22 bytes a round on a one-string struct.
//
// recv_borrow_fns_of answers the question on the CALLEE side, proving the
// receiver behaves exactly like a deep-drop-worthy local with the same three
// predicates reclaimable_names_of runs over one: body_unsafe_for (the box does
// not escape), moves_fields_stmts (no receiver-position hazard inside — so a
// method calling through its own receiver is refused, which is what keeps the
// registry non-circular), and optstruct_body_moves_field (no field reaches a
// bind / assign / return value, a non-borrowable argument, or a container).
//
// Both directions are pinned here. The flat cases prove the fields ARE freed;
// the refusal cases prove a method that really does move something out keeps
// the receiver's fields alive — each one reads the moved-out value after
// thousands of further rounds have recycled the freelist, so a wrongly granted
// deep drop returns garbage rather than merely leaking.
var recvBorrowDeepDropCases = []struct {
	name     string
	src      string
	expected int
}{
	// REFUSED at the CALL SITE — an identity-returning method whose result is
	// BOUND (`var alias = held.keep()`). The result may be the receiver's own
	// box, so `held` keeps its box-only release; the alias is read after 4000
	// further rounds, and a granted deep drop would have freed its tag.
	{"recvborrow-identity-return-safe", `struct Box { tag: string, n: i32 }
function (b: Box) keep(): Box { return b; }
function main(): i32 {
    var acc: i32 = 0;
    var held: Box = Box { tag: "start-tag-value", n: 1 };
    var alias: Box = held.keep();
    var i: i32 = 0;
    while (i < 4000) { var b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.keep().n) % 251; i = i + 1; }
    if (alias.tag.len() != 15) { return 95; }
    if (held.tag.len() != 15) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// REFUSED — a field MOVE. `label()` returns `b.tag`, handing the field to a
	// local that outlives the receiver, so optstruct_body_moves_field rejects
	// the method and `b` keeps its fields. The moved-out string is re-read at
	// the end.
	{"recvborrow-field-move-safe", `struct Box { tag: string, n: i32 }
function (b: Box) label(): string { return b.tag; }
function main(): i32 {
    var acc: i32 = 0;
    var src: Box = Box { tag: "start-tag-value", n: 1 };
    var moved: string = src.label();
    var i: i32 = 0;
    while (i < 4000) { var b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.label().len()) % 251; i = i + 1; }
    if (moved.len() != 15) { return 95; }
    if (moved[0:5] != "start") { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// REFUSED — the method calls through its OWN receiver (`b.inner()`), which
	// moves_fields_stmts marks. Admitting it would rest this entry on another,
	// so the registry declines rather than iterating a fixpoint. `via()` is a
	// pure read, so the refusal costs only the leak; the values stay correct.
	{"recvborrow-self-method-safe", `struct Box { tag: string, n: i32 }
function (b: Box) inner(): i32 { return b.n; }
function (b: Box) via(): i32 { return b.inner() + 1; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) { var b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.via()) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ADMITTED, and balanced: a scalar-reading method over a struct whose
	// string field is an ALIAS of a live local. The construction retains it, so
	// the now-granted deep drop DECS the dup rather than freeing the local's
	// box — the local is read after every round.
	{"recvborrow-aliased-field-balanced", `struct Box { tag: string, n: i32 }
function (b: Box) score(): i32 { return b.n * 2; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) {
        var nm: string = "start-tag-value";
        var b: Box = Box { tag: nm, n: i % 8 };
        acc = (acc + b.score()) % 251;
        if (nm.len() != 15) { return 95; }
        if (b.tag.len() != 15) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// THE OVER-RELEASE the "RECVRET:" credit gate closes. `keep = b.me()`
	// assigns the receiver's own box to a name that outlives the loop body, and
	// `b` was credited anyway — on the strength of a method receiver counting as
	// a borrow — so its per-rebind reclaim freed the box `keep` still reads.
	// Measured on the parent as a genuine over-release (__rc_underflow() ticked,
	// not a leak), which is why this row asserts 0 and not merely flatness:
	// exit 99 is the pre-fix outcome.
	{"recvret-rebound-outer-no-over-release", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function rounds(n: i32): i32 {
    var t: i32 = 0;
    var keep: Box = Box { tag: "outer-tag-value", n: 0 };
    for i in 0..n { var b: Box = Box { tag: "start-tag-value", n: i % 8 }; keep = b.me(); t = (t + b.n) % 251; }
    return t + keep.tag.len();
}
function main(): i32 {
    var acc: i32 = rounds(4000);
    if (acc < 15) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The same gate through a RETURN: `me()`'s result leaves the function, so
	// `b` cannot keep a credit that would free it on the way out.
	{"recvret-returned-result-safe", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function mk(k: i32): Box { var b: Box = Box { tag: "start-tag-value", n: k }; return b.me(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) { var r: Box = mk(i % 8); if (r.tag.len() != 15) { return 95; } acc = (acc + r.n) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The same gate through a CONTAINER: the result is stored into an array
	// literal, which outlives nothing here but is a move position all the same.
	{"recvret-container-result-safe", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) {
        var b: Box = Box { tag: "start-tag-value", n: i % 8 };
        var xs: Box[] = [b.me()];
        if (xs[0].tag.len() != 15) { return 95; }
        acc = (acc + xs[0].n) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ADMITTED with an rc-ARRAY field: the deep drop walks `items` as well as
	// `tag`. Read through the method and directly, both must stay valid.
	{"recvborrow-array-field-safe", `struct Box { tag: string, items: i32[] }
function (b: Box) total(): i32 { return b.items.len() + b.tag.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) { var b: Box = Box { tag: "start-tag-value", items: [1, 2, 3] }; if (b.total() != 18) { return 95; } acc = (acc + b.items[i % 3]) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// recvBorrowDeepDropLeakCases assert heap FLATNESS, so they are register-backend
// only — the wasm driver's own allocations sit between the two probes and the
// WAT leg reads __rc_underflow() instead (see the trap note in
// docs/RC-PERCEUS-SELF-HOST-PORT.md §9).
var recvBorrowDeepDropLeakCases = []struct {
	name string
	src  string
}{
	// The headline: a struct local handed to a borrowing method keeps its deep
	// drop, so its fresh string field is freed every round. 22 B/round before.
	{"recvborrow-deep-drop-flat", `struct Box { tag: string, n: i32 }
function (b: Box) score(): i32 { return b.n * 2; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.score()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The IDENTITY-return tier ("RECVIDENT:"): `me()` hands the receiver back on
	// its only path, and the result is consumed INLINE — read through to a
	// `.len()` and dead. Nothing outliving `b` holds it, so `b` keeps its deep
	// drop. 22 B/round before.
	{"recvident-inline-result-flat", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().tag.len()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A BORROWABLE param is not a move position, so `take(b.me())` keeps the
	// credit too — the same registry and reading fieldmove_expr already applies
	// to a field chain. Marking every argument cost this shape 72 B/round in an
	// intermediate of this slice, worse than the 22 it started at.
	{"recvident-borrowable-arg-flat", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function take(x: Box): i32 { return x.n; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + take(b.me())) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The same credit over an rc-ARRAY field: the deep walk frees the buffer.
	{"recvborrow-array-field-flat", `struct Box { tag: string, items: i32[] }
function (b: Box) total(): i32 { return b.items.len(); }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), items: [i, i + 1, i + 2] }; acc = (acc + b.total()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

// recvBorrowAllCases is the safety table plus the flatness table, for the
// register backends that can run both.
func recvBorrowAllCases() []struct {
	name     string
	src      string
	expected int
} {
	out := append([]struct {
		name     string
		src      string
		expected int
	}{}, recvBorrowDeepDropCases...)
	for _, lc := range recvBorrowDeepDropLeakCases {
		out = append(out, struct {
			name     string
			src      string
			expected int
		}{lc.name, lc.src, 0})
	}
	return out
}

// TestSelfHostRecvBorrowDeepDropX86_64 runs every case through the self-hosted
// x86-64 driver. 98 = the receiver's fields leaked, 99 = over-release,
// 95/96 = a value a refusal was protecting was corrupted.
func TestSelfHostRecvBorrowDeepDropX86_64(t *testing.T) {
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

	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = receiver fields leaked; 99 = over-release; 95/96 = a refused move was freed anyway)", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostRecvBorrowDeepDropArm64 runs the same cases through the arm64 IR
// driver under qemu — the second register backend, where the deep drop lands in
// the same place through a different emitter.
func TestSelfHostRecvBorrowDeepDropArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-ir", "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = receiver fields leaked; 99 = over-release; 95/96 = a refused move was freed anyway)", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostRecvBorrowDeepDropWasm runs the SAFETY cases on the wasm IR
// backend. The flatness cases are excluded: the WAT driver's own allocations
// sit between the probes, so __rc_underflow() is the witness there.
func TestSelfHostRecvBorrowDeepDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host receiver-borrow deep-drop wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range recvBorrowDeepDropCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("receiver-borrow deep drop wasm %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
