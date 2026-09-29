package e2eselfhost

import "testing"

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
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (slice_unchecked(moved, 0, 5) != "start") { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// THE OVER-RELEASE the "RECVRET:" credit gate closes. `keep = b.me()`
	// assigns the receiver's own box to a name that outlives the loop body, and
	// `b` was credited anyway — on the strength of a method receiver counting as
	// a borrow — so its per-rebind reclaim freed the box `keep` still reads.
	// Measured on the parent as a genuine over-release (__rc_underflow_count() ticked,
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
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (__rc_underflow_count() != 0) { return 99; }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// recvBorrowDeepDropLeakCases assert heap FLATNESS across the two
// __heap_bump_bytes() probes.
var recvBorrowDeepDropLeakCases = []struct {
	name string
	src  string
}{
	// The main case: a struct local handed to a borrowing method keeps its deep
	// drop, so its fresh string field is freed every round. 22 B/round before.
	{"recvborrow-deep-drop-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The IDENTITY-return tier ("RECVIDENT:"): `me()` hands the receiver back on
	// its only path, and the result is consumed INLINE — read through to a
	// `.len()` and dead. Nothing outliving `b` holds it, so `b` keeps its deep
	// drop. 22 B/round before.
	{"recvident-inline-result-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A BORROWABLE param is not a move position, so `take(b.me())` keeps the
	// credit too — the same registry and reading fieldmove_expr already applies
	// to a field chain. Marking every argument cost this shape 72 B/round in an
	// intermediate of this slice, worse than the 22 it started at.
	{"recvident-borrowable-arg-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The same credit over an rc-ARRAY field: the deep walk frees the buffer.
	{"recvborrow-array-field-flat", `import "std/i32";
struct Box { tag: string, items: i32[] }
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
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A counted handback in RECEIVER position — the fourth consumer that owes
	// the result's release, after a binding, a discarded statement and an
	// argument temp. `.val()` lets the receiver escape nowhere, so the count
	// this frame owes goes back once the call returns.
	{"recvident-chain-recv-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The same over an rc-ARRAY field: the shallow dec leaves the buffer to the
	// box's own drop, so the round still flattens.
	{"recvident-chain-recv-array", `import "std/i32";
struct Box { tag: string, items: i32[] }
function (b: Box) me(): Box { return b; }
function (b: Box) total(): i32 { return b.items.len(); }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), items: [i, i + 1, i + 2] }; acc = (acc + b.me().total()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// Two counted handbacks in a row: the inner result stands in the receiver
	// position of a method that hands the receiver back COUNTED, so the outer
	// call adds a count of its own and the inner's dec still leaves exactly one.
	{"recvident-chain-recv-double", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A strict-fresh FREE producer under the same chain: counted_call_key reads
	// a bare callee name too, so the receiver release is keyed the same way.
	{"recvident-chain-recv-freecall", `import "std/i32";
struct Box { tag: string, n: i32 }
function mk(i: i32): Box { return Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = mk(i); acc = (acc + b.me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

// recvBorrowAllCases is the safety table plus the flatness table.
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

const recvBorrowFailFmt = "%s exited %d, want %d (98 = receiver fields leaked; 99 = over-release; 95/96 = a refused move was freed anyway)"

// TestSelfHostRecvBorrowDeepDropX86_64 runs every case through the self-hosted
// CLI for x86-64.
func TestSelfHostRecvBorrowDeepDropX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostRecvBorrowDeepDropArm64 runs the same cases for arm64 under qemu,
// where the deep drop lands in the same place through a different emitter.
func TestSelfHostRecvBorrowDeepDropArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostRecvBorrowDeepDropWasm runs the same cases for wasm32-wasi.
func TestSelfHostRecvBorrowDeepDropWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}
