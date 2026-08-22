package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The bare-ident tuple element's retain, string limb (#7226) --------------
//
// The array limb landed with the string one left open, and the string one was
// the larger leak: 32 B/round unbounded, where the array's was 40 bounded.
//
//	(i32, string) from a bare ident   allocs=600 frees=200   live 6400 at 200 rounds
//	                                                              12800 at 400
//
// against 0 on native and interp. Exactly 2.0x per doubling — unbounded, not a
// constant. Measure it with `w("ab")` and never `"ab" + "c"`: the latter
// constant-folds to an immortal literal (rc = -1) and the probe measures nothing,
// which is why the issue's original table called this row clean.
//
// It was a CREDIT-side gap, not a missing release. lower_expr's ExprTuple arm
// already retained the element (slot_is_rc_container includes a string slot), but
// the string local at that element was escape-flagged by expr_unsafe_for, so it
// never earned "STR:" and its own box was never swept: inc 1, dec 0. Adding the
// element release ALONE would have balanced the tuple and still stranded the
// local, so both halves are needed and neither is useful without the other:
//
//   - the credit, via the interlock body_unsafe_for_clo already ran for closure
//     captures and union payloads — a bare-ident element of a tuple credited
//     "TUPELEMOK:" is no longer an escape ("TUPE:" + tuple_bare_ident_sole_use);
//   - the release, recorded as kind `s` and emitted as __fern_str_free.
//
// __fern_rc_dec is the WRONG helper here and would be heap corruption rather
// than a leak. A string box is {rc@base, data@base+8, len@base+16} with the value
// at base+8, so rc sits at value-8 for both layouts — which is why one
// __fern_rc_inc retains either — but rc_dec frees at value-16, eight bytes below
// the block.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test.
//
// The x86-64 leg is the one that carries the leak signal: six of its seven cases
// fail with the change reverted and the compiler rebuilt. The wasm and arm64 legs
// assert exit codes, which a leak does not move, so they pass either way — they
// are there to catch a release that frees a LIVE box on those backends, which
// does change the answer.

const tupStrElemW = "function w(a: string): string { return a + \"!\"; }\n"

const tupStrElemMain = "\nfunction main(): i32 { var x: i32 = 0; var r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return x % 83; }"

type tupStrElemCase struct {
	name string
	src  string
	want int
}

func tupStrElemCases() []tupStrElemCase {
	return []tupStrElemCase{
		{
			// The headline shape.
			name: "str_elem",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    return t.0 + t.1.len();
}` + tupStrElemMain,
			want: 21,
		},
		{
			// The source local is READ after the tuple is built. The release is at
			// scope exit, after every use, and it gives back the TUPLE's reference
			// — the local still holds its own, so the read must see live bytes.
			name: "str_read_after",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    return t.1.len() + s.len() + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			// Two string positions in one tuple — the release walks a list.
			name: "two_str_elems",
			src: tupStrElemW + `function round(i: i32): i32 {
    var a: string = w("ab");
    var b: string = w("cd");
    var t: (string, string) = (a, b);
    return t.0.len() + t.1.len() + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			// A string element and an ARRAY element in one tuple. The kinds string
			// carries both, and each position picks its own helper — the case a
			// single release helper for the whole walk gets wrong in the direction
			// that corrupts the heap rather than leaking.
			name: "mixed_str_and_array",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var xs: i32[] = [i, i + 1];
    var t: (string, i32[]) = (s, xs);
    return t.0.len() + t.1[0] + i;
}` + tupStrElemMain,
			want: 74,
		},
		{
			// A LITERAL-init string local (the "LITSTR:" class rather than the fresh
			// producers). It reaches the same interlock, since that is inside
			// body_unsafe_for_clo rather than at one caller.
			//
			// The one case here that was ALREADY balanced before the change, so it
			// pins nothing about the leak. It earns its place in the other
			// direction: this class now records kind `s` too, so a __fern_str_free
			// that mishandled the box behind a literal init would show up here as a
			// double free rather than as a leak.
			name: "litstr_elem",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = "abcd";
    var t: (i32, string) = (i, s);
    return t.1.len() + i;
}` + tupStrElemMain,
			want: 38,
		},
		{
			// The tuple's last mention is not the final statement, so the precise
			// drop-on-last-use claims the box instead of the exit sweep. It replays
			// the same kinds list, so the string limb reaches it for free — but only
			// because the list is what both consult.
			name: "last_use_before_return",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var acc: i32 = t.1.len();
    return acc + i;
}` + tupStrElemMain,
			want: 21,
		},
		{
			// The tuple is a cross-tuple reuse DONOR, so its box is recycled and the
			// release runs inside the uniqueness arm (#7275). The fifth site on the
			// enumeration reached by a string element.
			name: "cross_reuse_donor",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var a: i32 = t.1.len();
    var u: string = w("cde");
    var v: (i32, string) = (i, u);
    return a + v.1.len() + i;
}` + tupStrElemMain,
			want: 6,
		},
	}
}

// TestSelfHostTupleStrElemRetainX86_64 — the string element's retain is given
// back AND the source local earns its own sweep, so allocs and frees balance.
//
// Both halves show up in this one number. Without the credit the local's box is
// never swept (frees short); with the credit but no release the tuple's reference
// keeps it alive (frees short by the same amount); with a release but no credit
// the arithmetic balances on the tuple and strands the local.
func TestSelfHostTupleStrElemRetainX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupstrelem_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: __fern_str_free "+
					"claimed a box someone else still owns)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"A string built by constant folding is an immortal literal (rc = -1) "+
					"and measures nothing; every probe here goes through w()", tc.name)
			}
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0. The retain is per round, so "+
					"anything stranded here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleStrElemHazardsX86_64 — shapes the credit must REFUSE, plus the
// one whose point is surviving at all.
//
// These assert the ANSWER and the underflow counter, not leak counts, and
// deliberately so: a refused shape falls back to leak mode, which is the safe
// direction and the same trade the array limb makes. A wrongly-GRANTED credit
// here is a freed-then-read string, not a number.
//
// The underflow check is the load-bearing part. Without it every case below
// passes against a compiler that over-releases: a doubly-released block goes back
// to the freelist and the arithmetic still comes out right.
//
// Both wants came from bin/fern -interp and the native x86-64 backend agreeing.
func TestSelfHostTupleStrElemHazardsX86_64(t *testing.T) {
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
			// The element is EXTRACTED to a new local. rctuple_payload_escapes
			// refuses a bare pointer extraction, and "string" is not a scalar type
			// name, so the tuple never earns "TUPELEMOK:" — which also denies the
			// interlock, since it is keyed on that credit. Both halves stand down
			// together, which is what keeps them consistent.
			name: "str_elem_extracted_local",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var u: string = t.1;
    return u.len() + i;
}` + tupStrElemMain,
			want: 21,
		},
		{
			// The same extraction leaving the frame, where a wrongly-granted credit
			// frees a box the caller is about to read.
			name: "str_elem_extracted_escaping",
			src: tupStrElemW + `function grab(i: i32): string {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    return t.1;
}
function round(i: i32): i32 { var g: string = grab(i); return g.len() + i; }` + tupStrElemMain,
			want: 21,
		},
		{
			// The name appears at a bare-ident element AND somewhere else in the
			// same tuple. tuple_bare_ident_sole_use refuses it, so the second use
			// — which the interlock cannot see the shape of — is never hidden.
			name: "ident_used_twice_in_tuple",
			src: tupStrElemW + `function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (string, i32) = (s, s.len());
    return t.0.len() + t.1 + i;
}` + tupStrElemMain,
			want: 72,
		},
		{
			// The tuple is built on one branch only, so the slot still holds its
			// entry zero at the sweep. __fern_str_free null-guards the element; the
			// op_tuple_get that reaches it does not. A missing guard is a segfault,
			// so this case is about surviving at all.
			//
			// It leaks 1600 bytes, and that floor is #7292's — a block-scoped fresh
			// string local is swept by nothing, with or without a tuple in the block
			// (the same program with the tuple deleted measures allocs=100 frees=0).
			// Hence the answer-only assertion here.
			name: "untaken_branch_null",
			src: tupStrElemW + `function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i % 2 == 0) { var s: string = w("ab"); var t: (i32, string) = (i, s); acc = t.1.len(); }
    return acc + i;
}` + tupStrElemMain,
			want: 37,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupstrhaz_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (99 = rc underflow: the element release "+
					"claimed a reference the frame handed to another owner)", tc.name, exit, tc.want)
			}
		})
	}
}

// TestSelfHostTupleStrElemRetainWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer is what proves the release did
// not free a live box.
func TestSelfHostTupleStrElemRetainWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple string-element retain wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
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
			watFile := filepath.Join(dir, "tupstrelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple string-element retain wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleStrElemRetainIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleStrElemRetainIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupStrElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupstrelem_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
