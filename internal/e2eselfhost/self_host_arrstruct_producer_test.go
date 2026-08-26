package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An array-of-structs from a producer that returns a LOCAL ----------------
//
// The arrstruct twin of #7335. `collect_fresh_arrstruct_names` admits
// `var g: Val[] = mk(..)` off the "ARRSTRUCTF:" registry, and
// `fn_returns_fresh_arrstruct` built that registry by proving every return of
// the callee is a fresh array LITERAL — syntactically. The append-built form,
// which is how a producer that computes its elements has to be written, was
// refused:
//
//	function mk(i: i32): Val[] { return [Val { .. }, Val { .. }]; }        clean
//	function mk(i: i32): Val[] { var vals: Val[] = [];
//	                             vals = vals.append(Val { .. });
//	                             return vals; }                            leaks
//
// Same caller either way. The refused form left the consumer's slot uncredited,
// so its exit sweep took the shallow buffer dec: every element box and every
// element ARRAY field stranded. The leak needs no struct literal at the call
// site to appear — `var src: Val[] = mk(i); return src.len() + src[0].k;` is
// enough, which is what makes this an ordinary-code leak rather than a matrix
// corner. The construction-retain matrix's struct_arr__local / __param cells
// read as a construction-retain hole and were this instead: their `mkv` is
// append-built.
//
// Strictness is the registry's question, not the local credit's. Inside one
// frame an appended BARE IDENT is fine — the append retains it and both owners
// walk under __fern_rc_is_unique. A RETURNED container's co-owner would be a
// local of the frame being left, so the producer arm requires every appended
// element to be a fresh struct LITERAL, the same reason arrstruct_lit_is_fresh
// takes bare_ok=false for this registry. producer_bare_ident_elem pins that
// refusal: it stays a safe leak rather than becoming an over-release.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and
// the native x86-64 backend agreed on each — never read off the self-host run
// under test.

type arrstructProdCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrstructProdMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrstructProdDecl = "struct Val { kids: i32[], k: i32 }\n"

func arrstructProdCases() []arrstructProdCase {
	return []arrstructProdCase{
		{
			// The repro: the producer builds by self-append and returns the local.
			name: "producer_returns_local",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { var vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); vals = vals.append(Val { kids: [i + 2], k: i }); return vals; }
function round(i: i32): i32 { var v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// The same producer returning the literal directly — admitted before
			// this change, and the diff that isolated the cause.
			name: "producer_returns_literal",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { return [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; }
function round(i: i32): i32 { var v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// No producer at all: the literal bound straight into the local. Always
			// credited; must stay so.
			name: "literal_init",
			src: arrstructProdDecl + `function round(i: i32): i32 { var v: Val[] = [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; return v.len() + v[0].k; }` +
				arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD, the shape #7335 recorded as the one a careless
			// widening breaks: two same-named `v`, one from the producer and one a
			// bare alias of a parameter. The arrstruct credit is site-keyed already,
			// so the alias cannot inherit it — asserted, not assumed. 99 here would
			// be main's `b` freed under it, and no byte count would say so.
			name: "sibling_alias",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { var vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); return vals; }
function round(base: Val[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: Val[] = mk(i);  t = t + v.len() + v[0].k; }
    if (i % 2 == 1) { var v: Val[] = base;   t = t + v.len() + v[0].k; }
    return t;
}
function main(): i32 { var b: Val[] = [Val { kids: [7, 8], k: 9 }]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 12,
		},
		{
			// STRICTNESS, refused deliberately: the appended element is a bare IDENT,
			// so the returned container's counted co-owner is a local of the frame
			// being left. Admitting it would free `e`'s box twice. Stays a safe leak.
			name: "producer_bare_ident_elem",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { var e: Val = Val { kids: [i, i + 1], k: i }; var vals: Val[] = []; vals = vals.append(e); return vals; }
function round(i: i32): i32 { var v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			// Refused: a reassignment that is not a self-store. `vals` may hold
			// another producer's structure by the return, which this frame does not
			// own outright.
			name: "producer_foreign_rebind",
			src: arrstructProdDecl + `function other(i: i32): Val[] { return [Val { kids: [i], k: i }]; }
function mk(i: i32): Val[] { var vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); if (i % 3 == 0) { vals = other(i); } return vals; }
function round(i: i32): i32 { var v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			// Refused: the local escapes by a route other than the return, so the
			// callee cannot promise the caller owns it outright.
			name: "producer_local_escapes",
			src: arrstructProdDecl + `function sink(vs: Val[]): i32 { return vs.len(); }
function mk(i: i32): Val[] { var vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); var n: i32 = sink(vals); return vals; }
function round(i: i32): i32 { var v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
	}
}

// TestSelfHostArrStructProducerX86_64 — an array-of-structs from a local-returning
// producer is reclaimed, and neither a same-named sibling nor a refused producer
// shape starts over-releasing.
func TestSelfHostArrStructProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrstructProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a share the "+
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
				t.Errorf("%s: %s — must balance at live_bytes 0. Each element box and "+
					"its kids array are two thirds of the allocations, so a withheld "+
					"deep walk shows as frees at one third of allocs", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArrStructProducerWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostArrStructProducerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arrstruct producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrstructProdCases() {
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
			watFile := filepath.Join(dir, "arrstructprod_"+tc.name+".wat")
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
