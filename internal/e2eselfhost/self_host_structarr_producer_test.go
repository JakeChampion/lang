package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A SCALAR-FIELD struct array from a producer call (#7445) ----------------
//
// `collect_fresh_structarr_names` admitted an array LITERAL and an append-built
// local, and nothing else. A producer call was not a shape it refused on the
// merits — there was no producer registry for this element kind at all, where
// every neighbouring one has had one for a while ("ARRSTRUCTF:" for structs with
// an rc-array field, "ARRTUPF:" for tuples, "ARENUMF:" for enums). So:
//
//	var v: P[] = [P { .. }, P { .. }];   // credited, flat
//	var v: P[] = mk();                   // uncredited, 160 B per round
//
// Same elements, same frame, same free. The uncredited binding fell through to
// the generic shallow buffer dec, which frees the outer buffer and no element
// box, so every element struct — and, for a string-fielded element, its string —
// stranded. Unbounded in any loop that constructs one, which is the shape of an
// AST walker and so of this compiler.
//
// The fix registers "STRUCTARRF:<name>" off the SAME body proof its rc-array-field
// sibling uses (every return either a fresh struct-literal array or a local built
// by self-append and handed back), which is why the append-built producer row
// below is green as well.
//
// The refusal rows are the ones that carry the soundness. A producer whose
// element is a bare IDENT, one that hands back its own parameter, and one whose
// element field embeds a caller's string all stay UNcredited: their leak is the
// old one, and 99 in any of those rows would be a box freed under a live owner.
// Byte counts cannot see that difference — a double free and a clean run report
// identical allocs/frees — so every row asserts an exit code, and only the rows
// that must balance assert bytes.
//
// Every want below was confirmed against BOTH oracles, the native x86-64 backend
// and `bin/fern -interp`, which agreed on each; none was read off the self-host
// run under test.

type structarrProdCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const structarrProdDecl = "struct P { s: string, n: i32 }\n" +
	"function w(a: string): string { return a + \"!\"; }\n"

const structarrProdMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func structarrProdCases() []structarrProdCase {
	return []structarrProdCase{
		{
			// The repro: the producer binds the literal to a local and returns it.
			name: "producer_returns_local",
			src: structarrProdDecl + `function mk(): P[] { var a: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return a; }
function round(i: i32): i32 { var v: P[] = mk(); return v.len(); }` + structarrProdMain,
			want: 68, balance: true,
		},
		{
			// The same producer returning the literal directly. Leaked identically
			// before the fix — the discriminator here is the CALL, not the form of
			// the return, which is what separates this from its arrstruct sibling.
			name: "producer_returns_literal",
			src: structarrProdDecl + `function mk(): P[] { return [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; }
function round(i: i32): i32 { var v: P[] = mk(); return v.len(); }` + structarrProdMain,
			want: 68, balance: true,
		},
		{
			// No producer: the literal bound straight into the local. Credited all
			// along, and the control that says the elements themselves are fine.
			name: "literal_init",
			src: structarrProdDecl + `function round(i: i32): i32 { var v: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return v.len(); }` +
				structarrProdMain,
			want: 68, balance: true,
		},
		{
			// The append-built producer, which the shared body proof admits for
			// free. It is how a producer that computes its elements has to be
			// written, so a literal-only registry would have left the common form
			// leaking after the headline shape was fixed.
			name: "producer_append_built",
			src: structarrProdDecl + `function mk(i: i32): P[] { var a: P[] = []; a = a.append(P { s: w("p"), n: i }); a = a.append(P { s: w("q"), n: i }); return a; }
function round(i: i32): i32 { var v: P[] = mk(i); return v.len() + v[0].n; }` + structarrProdMain,
			want: 48, balance: true,
		},
		{
			// The result is read back element-wise, and a second fresh array is
			// built alongside it in the same frame. A box freed early would be
			// recycled into the second array, so the answer discriminates where a
			// byte count would not.
			name: "readback_beside_fresh",
			src: structarrProdDecl + `function mk(): P[] { var a: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return a; }
function round(i: i32): i32 {
    var v: P[] = mk();
    var junk: P[] = [P { s: w("zz"), n: 9 }, P { s: w("yy"), n: 8 }];
    return v[0].s.len() + v[1].n + junk[0].n;
}` + structarrProdMain,
			want: 27, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD: two same-named `v`, one from the producer and
			// one a bare alias of a parameter. The credit is site-keyed, so the
			// alias cannot inherit it — asserted, not assumed. 99 here would be
			// main's `b` freed under it, and no byte count would say so.
			name: "sibling_alias",
			src: structarrProdDecl + `function mk(i: i32): P[] { var a: P[] = [P { s: w("p"), n: i }]; return a; }
function round(base: P[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: P[] = mk(i); t = t + v.len() + v[0].n; }
    if (i % 2 == 1) { var v: P[] = base;  t = t + v.len() + v[0].n; }
    return t;
}
function main(): i32 {
    var b: P[] = [P { s: w("base"), n: 7 }];
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 78, balance: true,
		},
		{
			// REFUSED, deliberately: the appended element is a bare IDENT, so the
			// returned container's counted co-owner is a local of the frame being
			// left. Admitting it would free `e`'s box twice. Stays a safe leak.
			name: "producer_bare_ident_elem",
			src: structarrProdDecl + `function mk(i: i32): P[] { var e: P = P { s: w("p"), n: i }; var a: P[] = []; a = a.append(e); return a; }
function round(i: i32): i32 { var v: P[] = mk(i); return v.len() + v[0].n; }` + structarrProdMain,
			want: 14,
		},
		{
			// REFUSED: the producer hands back its own PARAMETER, so the array the
			// caller binds is one the caller already owns. Crediting the binding
			// would release it twice.
			name: "producer_returns_param",
			src: structarrProdDecl + `function passthru(a: P[]): P[] { return a; }
function round(i: i32): i32 {
    var src: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }];
    var v: P[] = passthru(src);
    return v.len() + src.len();
}` + structarrProdMain,
			want: 53,
		},
		{
			// ADMITTED, and the element walk still declines: the element's string
			// field is the CALLER's string, which the deep drop reaches under
			// __fern_rc_is_unique and so leaves alone. The string is read back after
			// the array is gone — a freed box would answer with junk or underflow.
			name: "producer_param_string_field",
			src: structarrProdDecl + `function mkp(nm: string): P[] { return [P { s: nm, n: 1 }]; }
function round(i: i32): i32 {
    var owned: string = w("keepvalue");
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < 4) {
        var v: P[] = mkp(owned);
        acc = acc + v.len() + v[0].s.len();
        j = j + 1;
    }
    var junk1: string = w("ZZZZZZZZZZ");
    var junk2: string = w("YYYYYYYYYY");
    return acc + owned.len() + junk1.len() + junk2.len();
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 47,
		},
		{
			// The rc-ARRAY-field element struct, which "ARRSTRUCTF:" owns. Here to
			// pin the two registries disjoint: the new one must not claim it, and
			// it must stay balanced.
			name: "arrfield_elem_stays_arrstruct",
			src: `struct Q { ys: i32[], n: i32 }
function mkq(): Q[] { var a: Q[] = [Q { ys: [1, 2], n: 1 }, Q { ys: [3], n: 2 }]; return a; }
function round(i: i32): i32 { var v: Q[] = mkq(); return v.len() + v[0].ys.len(); }` + structarrProdMain,
			want: 53, balance: true,
		},
	}
}

// TestSelfHostStructArrProducerX86_64 — a scalar-field struct array bound from a
// producer call is reclaimed whole, and no refused producer shape starts
// over-releasing.
func TestSelfHostStructArrProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structarrprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a reference the "+
					"producer registry admitted without owning it)", tc.name, exit, tc.want)
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
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0. The two element boxes "+
					"and the outer buffer are what an uncredited binding strands, so a "+
					"withheld element walk shows as frees at one seventh of allocs",
					tc.name, summary)
			}
		})
	}
}

// TestSelfHostStructArrProducerWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostStructArrProducerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping structarr producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
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
			watFile := filepath.Join(dir, "structarrprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStructArrProducerIRArm64 — the arm64 leg, the only one where the
// self-host toolchain assembles and links the binary itself.
func TestSelfHostStructArrProducerIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structarrprod_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s: arm64 exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
