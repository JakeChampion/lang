package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A `Some(None)` payload loses the binding's declared type (#7217) --------
//
// `some_opt_type` types a direct `Some(x)` construction from its ARGUMENT, and
// elem_type_tag cannot see through a bare `None`: it is an ExprIdent with no
// slot, so it falls to the "i32" default and `Some(None)` answers
// "Option[i32]". That beat the declared type of
//
//	var o: Option[Option[i32]] = Some(None);
//
// so the `Some(inner)` arm bound the inner Option BOX as a scalar and the
// nested `match (inner)` had an untyped scrutinee, bailing the whole function:
//
//	FERN_STRICT_IR: f (did not lower: `match`)
//
// Three neighbours isolated it — only the third bailed, and the last is the
// tell, since routing the same `None` through an annotated local restores the
// type elem_type_tag could not read off the construction:
//
//	Some(None)     + a SINGLE-level match      lowers
//	Some(Some(i))  + a nested match            lowers
//	Some(None)     + a nested match            BAILS
//	var inner0: Option[i32] = None; Some(inner0) + a nested match   lowers
//
// The fix is a precedence one: the construction walk is consulted only for an
// UNANNOTATED binding. An annotation names the payload outright and the checker
// has already proven the construction assignable to it, so it is left to the
// annotation fallback that already existed below.
//
// The fabrication was directly observable: an unannotated `Some(None)` whose
// payload IS used reports `calls unknown symbol "i32.is_none"` — the arm
// binding really was an i32.
//
// The unannotated rows are the guard on that precedence. `var o = Some(None)`
// types its payload as an unresolved type var the checker accepts only while
// the payload goes unread (using it is E002/E043), and the construction walk is
// the only thing that types it — so it must keep answering there.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test. Native is fully balanced on all six.
type nestedOptNoneCase struct {
	name      string
	src       string
	want      int
	balance   bool // assert allocs == frees at live_bytes 0
	wantFrees int  // when non-zero, assert an exact free count
}

const nestedOptNoneMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

func nestedOptNoneCases() []nestedOptNoneCase {
	return []nestedOptNoneCase{
		{
			// THE REPRO. Bailed the whole function; now lowers and balances.
			name: "nested_none",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(None);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19, balance: true,
		},
		{
			// The nested-Some neighbour: some_opt_type recurses through a `Some`
			// payload, so this always lowered. It must stay balanced — the
			// annotation now supplies the same type the recursion did.
			name: "nested_some",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 63, balance: true,
		},
		{
			// A nested RESULT payload. some_opt_type declines an Ok/Err payload
			// outright (it cannot name the Result's E arm), so this already went
			// through the annotation — the path this fix widens `None` onto.
			name: "nested_result",
			src: `function round(i: i32): i32 {
    var o: Option[Result[i32, i32]] = Some(Ok(i));
    match (o) {
        Some(inner) => { match (inner) { Ok(v) => { return v; }, Err(e) => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 63, balance: true,
		},
		{
			// The same `None` routed through an annotated local — the neighbour
			// that identified the cause, since here elem_type_tag reads the type
			// off the SLOT and always answered correctly. It leaks the extra
			// local's own box (400/0, 16000) — byte-for-byte what it leaked on
			// the parent, so exit code only.
			name: "via_local",
			src: `function round(i: i32): i32 {
    var inner0: Option[i32] = None;
    var o: Option[Option[i32]] = Some(inner0);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19,
		},
		{
			// THE PRECEDENCE GUARD. No annotation, so the construction walk is
			// still the only source of the slot's opt_type; gating it on the
			// annotation must not take this away. Leaks 400/0 exactly as on the
			// parent, so exit code only — what is pinned here is that it still
			// LOWERS.
			name: "unannotated_none",
			src: `function round(i: i32): i32 {
    var o = Some(None);
    match (o) { Some(inner) => { return 7; }, None => { return 2; } }
    return 0;
}` + nestedOptNoneMain,
			want: 72,
		},
		{
			// The annotated single-level match: lowered before via the fabricated
			// "Option[i32]", and lowers now via the annotation.
			name: "single_level",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(None);
    match (o) { Some(inner) => { return 7; }, None => { return 2; } }
    return 0;
}` + nestedOptNoneMain,
			want: 72, balance: true,
		},
		{
			// THE TAG GUARD. This column is a lowering TAG, not the declared
			// type: elem_type_tag gives a scalar-array payload a bare "i32"
			// where the annotation spells `Option[i32[]]`, deliberately —
			// some_opt_type's own comment says `Option[i32[][]]` depends on it.
			// So the annotation may win ONLY for the payload the walk cannot
			// read at all; letting it win generally drops this payload's
			// reclaim while every count still looks plausible.
			//
			// The escape is what makes the row discriminate, and it is why the
			// simpler `match (o) { Some(a) => a[0] + a[1] }` is not the probe
			// here: that shape balances under the broad rule too and would gate
			// nothing. With `a` escaping to an outer local, frees are 400 on the
			// parent and here, and 200 under the broad rule — which is the same
			// defect that took TestSelfHostNestedMatchBorrowHazards from 200
			// frees to 100. Pinned exactly, so an OVER-release is caught too.
			//
			// The 8000 live bytes are a pre-existing 40 B/round leak on this
			// shape, byte-identical on the parent; native frees all 600.
			name: "array_payload_escapes",
			src: `function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { held = a; acc = a[0]; }, None => {} }
    }
    return acc + held[1];
}` + nestedOptNoneMain,
			want: 77, wantFrees: 400,
		},
		{
			// A STRING payload. Newly lowering, and it leaks 80 B/round — but
			// that is the Option[string] reclaim gap, not this fix: a FLAT
			// `var o: Option[string] = Some(w("ab"))` leaks 600/0 on the parent
			// too, with no nesting anywhere. Asserted on the exit code only, so
			// the shape is pinned against becoming an OVER-release (99) while it
			// waits on that gap; native balances it at 200/200.
			name: "nested_string",
			src: `function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(None);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19,
		},
		{
			// A REASSIGNED Option local, likewise newly lowering and likewise
			// leaking for a pre-existing reason: reassigning an Option local
			// frees nothing, and the all-Some form of exactly this shape leaks
			// the same 120 B/round on the parent. Exit code only, same rationale.
			name: "reassigned",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(None);
    if (i % 2 == 0) { o = Some(Some(i)); }
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 74,
		},
	}
}

// TestSelfHostNestedOptNoneX86_64 — a `Some(None)` payload keeps the binding's
// declared Option type, so the nested `match` lowers instead of bailing.
func TestSelfHostNestedOptNoneX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedOptNoneCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "nestoptnone_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; the whole "+
					"function bailed `did not lower: match` before this fix)", tc.name, exit, tc.want)
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
			if tc.wantFrees != 0 && frees != int64(tc.wantFrees) {
				t.Errorf("%s: %s — want exactly %d frees. A LOWER count is the "+
					"payload's reclaim dropped by overriding its lowering tag "+
					"with the declared type; a higher one is an over-release",
					tc.name, summary, tc.wantFrees)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
		})
	}
}

// TestSelfHostNestedOptNoneWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostNestedOptNoneWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested Some(None) wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedOptNoneCases() {
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
			watFile := filepath.Join(dir, "nestoptnone_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("nested Some(None) wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostNestedOptNoneIRArm64 — the arm64 sibling under qemu.
func TestSelfHostNestedOptNoneIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedOptNoneCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "nestoptnone_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("nested Some(None) arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
