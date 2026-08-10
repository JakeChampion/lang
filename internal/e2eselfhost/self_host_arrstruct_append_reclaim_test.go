package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Append-built ARRAY-OF-STRUCTS (deep class) element reclaim (#6535) ------
//
// The DEEP arrstruct class reclaims a `(<struct-with-an-rc-array-field>)[]`
// local by walking each element: __struct_drop_<T> for its rc fields, the
// struct box, then the outer buffer. Its credit refused any REASSIGNED name —
// and `vals = vals.append(Val { .. })` is a reassignment — so an append-built
// array-of-structs earned no credit at all and leaked its whole structure,
// while the literal-built form of the identical value was flat. That is the
// same hole #6127 closed for the SHALLOW structarr class; this is the deep
// sibling.
//
// Two things had to move together, and the second is the one that actually bit:
//
//   - the blunt not-reassigned exclusion becomes arrstruct_unsafe_for, which
//     sanctions the self-append rebind and refuses every other reassignment;
//   - arrstruct_elem_payload_escapes had to learn the self-append RECEIVER.
//     A bare mention of the array is an escape there, and `vals.append(..)`'s
//     receiver is a bare mention, so the credit was refused even once the
//     reassignment gate allowed it. `.len()` on the array carries exactly this
//     whitelist already, for exactly this reason (#6127).
//
// The element rule is unchanged from the literal-built path: a fresh no-base
// struct literal, so the element box is sole-owned and the deep walk frees only
// what the array owns. A BOUND element (`var v = Val { .. }; vals.append(v)`)
// is still refused — the live local and the buffer would both release the
// element's field buffers — which is the move-site half of #6535 and a
// separate mechanism.

const arrStructAppendChurnSrc = `struct Val { kind: i32, kids: i32[] }

function build(n: i32): i32 {
    var vals: Val[] = [];
    var i: i32 = 0;
    while (i < 4) { vals = vals.append(Val { kind: i, kids: [i, i + 1] }); i = i + 1; }
    return vals.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + build(r); r = r + 1; }
    return t / 100;
}`

// TestSelfHostArrStructAppendReclaimX86_64 — an append-built array-of-structs
// whose element carries an rc-array field reclaims its whole structure. The
// leak scales with the round count, so any nonzero live_bytes here is
// unbounded in a loop.
func TestSelfHostArrStructAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, arrStructAppendChurnSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "arrstruct_append", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 4 {
		t.Fatalf("program exited %d, want 4", exit)
	}

	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("program allocated nothing — the probe is not exercising the path")
	}
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an append-built array-of-structs must "+
			"reclaim its element field buffers, boxes and outer buffer, exactly as the "+
			"literal-built form of the same value does", summary, live)
	}
}

// TestSelfHostArrStructAppendHazardsX86_64 — the shapes the widened credit must
// still REFUSE, asserted through BEHAVIOUR. A wrongly-granted credit here frees
// a field buffer something else still points at, so the failure mode is a wrong
// answer or a crash, not a leak; a leak-based assertion would not see it.
func TestSelfHostArrStructAppendHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// A bound ELEMENT aliases a struct box whose `kids` buffer the
			// deep walk would free.
			name: "element_alias",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var vals: Val[] = [];
    vals = vals.append(Val { kind: 2, kids: [7, 8] });
    var q: Val = vals[0];
    return q.kids.len() + q.kind + vals.len();
}`,
			want: 5,
		},
		{
			// An IDENT element: the array does not solely own the box, so the
			// deep walk would free `shared`'s own `kids`.
			name: "ident_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var shared: Val = Val { kind: 5, kids: [1, 2, 3] };
    var vals: Val[] = [];
    vals = vals.append(shared);
    return vals[0].kind + shared.kids.len();
}`,
			want: 8,
		},
		{
			// A bare element-field PAYLOAD extraction binds the inner buffer —
			// arrstruct_elem_payload_escapes must still catch it now that the
			// append receiver no longer trips it.
			name: "payload_extract",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var vals: Val[] = [];
    vals = vals.append(Val { kind: 1, kids: [4, 5] });
    var keep: i32[] = vals[0].kids;
    return keep.len() + keep[0] + vals.len();
}`,
			want: 7,
		},
		{
			// The array ESCAPES by return, so the callee must not free it.
			name: "escaping_return",
			src: `struct Val { kind: i32, kids: i32[] }
function build(n: i32): Val[] {
    var vals: Val[] = [];
    vals = vals.append(Val { kind: n, kids: [n, n + 1] });
    return vals;
}
function main(): i32 { var r: Val[] = build(3); return r[0].kind + r[0].kids.len(); }`,
			want: 5,
		},
		{
			// A non-append reassignment must sink the credit outright — the
			// sanctioned rebind is the self-append and nothing else.
			name: "rebound_to_other",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var vals: Val[] = [];
    vals = vals.append(Val { kind: 4, kids: [4] });
    var qs: Val[] = [Val { kind: 9, kids: [9, 9] }];
    vals = qs;
    return vals[0].kind + qs[0].kids.len();
}`,
			want: 11,
		},
		{
			// The appended element is a `...base` copy, which shares the base's
			// field pointers — not a fresh sole owner, so the credit must be
			// refused and `base.kids` must survive.
			name: "base_copy_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var base: Val = Val { kind: 2, kids: [6, 7] };
    var vals: Val[] = [];
    vals = vals.append(Val { ...base, kind: 3 });
    return vals[0].kind + base.kids.len();
}`,
			want: 5,
		},
		{
			// The appended element's field is read OUT OF the array being built
			// — the arg walk must still see it, so the credit is refused.
			name: "self_sourced_element",
			src: `struct Val { kind: i32, kids: i32[] }
function main(): i32 {
    var vals: Val[] = [Val { kind: 1, kids: [3, 4] }];
    vals = vals.append(Val { kind: 2, kids: vals[0].kids });
    return vals[1].kids.len() + vals[0].kind;
}`,
			want: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "arrstruct_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"deep reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
