package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- A nested Option/Result local is never reclaimed (#7218) ----------------
//
// One level of Option is fully reclaimed; nesting one inside another dropped the
// reclaim entirely — frees=0, not merely incomplete:
//
//	Option[Option[i32]]  Some(Some(i))   allocs=400 frees=0 live_bytes=16000
//	Option[i32]          Some(i)         allocs=200 frees=200 live_bytes=0
//
// against 0 on native for both. Three gates refused it in series, and each had
// to be found by instrumenting the gate rather than reading the code: the
// candidate was never produced (rcpayload_option_cand knows array / string /
// string[] / struct payloads and nothing else), then the arm's nested
// `match (inner)` read as an ESCAPE, then `blockable` excluded it from the only
// pass that sees a loop-body local.
//
// A SCALAR inner box holds a by-value scalar and NOTHING else, so one
// __fern_rc_dec releases it whole — which is already what opt_payload_freefn
// answers for the type, making that half admission-only. An RC inner owns a
// payload of its own and takes emit_nested_opt_payload_drop instead: spill the
// inner box, free ITS payload, free the inner box, then the outer. Both halves
// are here; what separates them is the extra proof the rc one carries, that the
// NESTED match's own binding does not escape either (a pointer can outlive its
// arm where a scalar copy cannot).

func nestedOptChurn(prelude, body string, rounds int) string {
	return fmt.Sprintf(`%sfunction churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
%s
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, prelude, body, rounds)
}

// nestedOptFlatCases must reach live_bytes == 0 with allocs == frees — both
// boxes released every round, exactly as native does. Both `want`s of every case
// were adjudicated against BOTH oracles (`bin/fern -interp` and the native
// x86-64 backend), never read off the self-host run under test.
var nestedOptFlatCases = []struct {
	name    string
	body    string
	want100 int
	want200 int
}{
	{
		// The shape itself. `inner` is consumed by a nested match, which is a
		// BORROW — it reads the tag and payload and retains nothing — where the
		// coarse walker called any bare ident an escape.
		name: "option_inner",
		body: `        var o: Option[Option[i32]] = Some(Some(i));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		// A RESULT inner. Both arms must be scalar, because the drop runs after
		// the match and does not read the tag — an Err arm carrying a pointer
		// would be stranded by the very same dec that fully releases a scalar
		// one. type_is_scalar_union is what proves both.
		name: "result_inner",
		body: `        var o: Option[Result[i32, i32]] = Some(Ok(i));
        match (o) {
            Some(inner) => { match (inner) { Ok(v) => { acc = (acc + v) % 91; }, Err(e) => { acc = (acc + e) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		// No binding at all (`Some(_)`). Pins that the credit rides the
		// declaration, not some use of the payload.
		name: "unused_binding",
		body: `        var o: Option[Option[i32]] = Some(Some(i));
        match (o) { Some(_) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`,
		want100: 9,
		want200: 18,
	},
	{
		// The rc-inner half: THREE allocations a round — the array buffer, the
		// inner box, the outer box — and the two-level drop must free all three.
		// allocs == frees is what says it did.
		name: "rc_inner_array_literal",
		body: `        var o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len() + v[0]) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 54,
		want200: 7,
	},
	{
		// The same with the buffer coming from a LIVE LOCAL. Construction
		// rc_inc's an array slot, so the buffer is at rc 2 and the drop's dec is
		// the second — the interlock, not a double free. The underflow term in
		// the exit code is what proves that rather than the byte count.
		name: "rc_inner_array_ident",
		body: `        var xs: i32[] = [i, i + 1];
        var o: Option[Option[i32[]]] = Some(Some(xs));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
	{
		// A string inner, released by the rc-aware __fern_str_free (immortal
		// skips, shared decs, unique frees) rather than the array dec — on asm a
		// string box's data buffer is separate and its block class differs.
		name: "rc_inner_string",
		body: `        var o: Option[Option[string]] = Some(Some("ab"));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
	{
		// A string[] inner: __fern_str_arr_free walks the elements, where the
		// flat dec would strand every element box.
		name: "rc_inner_string_array",
		body: `        var o: Option[Option[string[]]] = Some(Some(["a", "b"]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
}

// nestedOptHazardCases are the shapes that must keep leaking. Each asserts the
// ANSWER, not bytes: freeing any of these would release a box a second owner
// still reads, which is an over-release rather than a leak — and every `want` is
// well under 100, so the `__rc_underflow_count() * 100` term in `main` cannot be
// mistaken for one.
var nestedOptHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		// The arm binding is MOVED OUT to an outer local. The nested-match
		// reading relaxes the scrutinee position and nothing else, so this is
		// still an escape.
		name: "binding_escapes",
		src: nestedOptChurn("", `        var keep: Option[i32] = None;
        var o: Option[Option[i32]] = Some(Some(i));
        match (o) { Some(inner) => { keep = inner; }, None => { acc = (acc + 2) % 91; } }
        match (keep) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 3) % 91; } }`, 200),
		want: 62,
	},
	{
		// The inner box comes from a LIVE LOCAL rather than a construction. That
		// local's own scalar-Option reclaim already frees it; this drop would be
		// the second. opt_arg_is_direct_ctor is what excludes it.
		name: "inner_is_local",
		src: nestedOptChurn("", `        var inner0: Option[i32] = Some(i);
        var o: Option[Option[i32]] = Some(inner0);
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`, 200),
		want: 62,
	},
	{
		// Bound from a CALL, so the box is not this frame's to free.
		name: "bound_from_call",
		src: nestedOptChurn("function mk(i: i32): Option[Option[i32]] { var q: Option[Option[i32]] = Some(Some(i)); return q; }\n",
			`        var o: Option[Option[i32]] = mk(i);
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`, 200),
		want: 62,
	},
	{
		// THE rc-inner trap. The NESTED match's binding is carried out of its
		// arm — a pointer, which a scalar inner's copy could never be — so the
		// two-level drop would free a buffer `held` still names.
		// nested_opt_payload_arm_escapes is what sees this; the outer arm looks
		// clean to every other gate.
		name: "inner_payload_escapes",
		src: nestedOptChurn("", `        var held: i32[] = [];
        var o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { held = v; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }
        acc = (acc + held.len()) % 91;`, 200),
		want: 36,
	},
	{
		// A `None` INNER under an rc-inner annotation. The two-level drop reads
		// offset 8 of the inner box unconditionally, and a None box never stored
		// one — opt_arg_is_some_ctor is what keeps this out.
		name: "inner_none_under_rc_type",
		src: nestedOptChurn("", `        var o: Option[Option[i32[]]] = Some(None);
        match (o) { Some(inner) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`, 200),
		want: 18,
	},
	{
		// THREE levels. nested_opt_inner_freefn names a release for an array /
		// string / string[] inner and nothing else, so an Option inner refuses
		// rather than recursing — the walk is two levels deep by construction.
		name: "triple_nested",
		src: nestedOptChurn("", `        var o: Option[Option[Option[i32]]] = Some(Some(Some(i)));
        match (o) { Some(inner) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`, 200),
		want: 18,
	},
	{
		// The INNER BOX itself extracted to an outer local, so the outer arm's
		// binding escapes under the scrutinee-borrow reading too.
		name: "inner_box_extracted",
		src: nestedOptChurn("", `        var o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        var keep: Option[i32[]] = None;
        match (o) { Some(inner) => { keep = inner; }, None => { acc = (acc + 2) % 91; } }
        match (keep) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 3) % 91; } }`, 200),
		want: 36,
	},
}

// TestSelfHostNestedOptionReclaimX86_64 is the leak gate. live_bytes == 0 with
// allocs == frees is the load-bearing assertion: frees short of allocs is the
// leak this closes, and frees ABOVE allocs would mean two paths claimed the same
// box, which is a double free rather than a leak.
//
// Non-vacuity: all three cases fail this against the parent commit, at
// frees=0 / live_bytes=16000 per 200 rounds.
func TestSelfHostNestedOptionReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedOptFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			check := func(rounds, want int) {
				t.Helper()
				src := nestedOptChurn("", tc.body, rounds)
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				name := fmt.Sprintf("nestedopt_%s_%d", tc.name, rounds)
				stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, name, asm))
				if exit != want {
					t.Fatalf("%s exited %d, want %d (a value >= 100 is an rc over-release, not a leak)",
						name, exit, want)
				}
				summary := ""
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, "leakcheck: ") {
						summary = line
					}
				}
				if summary == "" {
					t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
				}
				var allocs, frees, live int64
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatalf("%s: parse %q: %v", name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
				}
				t.Logf("%s: %s", name, summary)
				if live != 0 {
					t.Errorf("%s live_bytes=%d, want 0 — a nested Option local leaks BOTH boxes "+
						"every round where one level of Option is fully reclaimed (#7218)", name, live)
				}
				if allocs != frees {
					t.Errorf("%s allocs=%d frees=%d — must balance exactly", name, allocs, frees)
				}
			}
			check(100, tc.want100)
			check(200, tc.want200)
		})
	}
}

// TestSelfHostNestedOptionHazardsX86_64 pins the refusals. A wrong answer or a
// crash here means a box was freed while its construction site, an extracted
// local, or the caller still owned it. Each `want` came from the interpreter and
// the native backend agreeing.
func TestSelfHostNestedOptionHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedOptHazardCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			bin := buildBin(t, gcc, dir, "nestedopt_hazard_"+tc.name, asm)
			if _, exit := hevRun(t, runner, bin); exit != tc.want {
				t.Errorf("exited %d, want %d — %d+100 would be an rc over-release, and a crash "+
					"means a box was freed under a second owner", exit, tc.want, tc.want)
			}
		})
	}
}

// nestedOptAllCases is every case above for the backend legs, which check the
// ANSWER and the over-release counter rather than bytes: the self-host x86-64
// emitter is the only one carrying the leakcheck census (asm_ir.fern's
// leak_check_on has no arm64 or wasm sibling).
func nestedOptAllCases() []struct {
	name string
	src  string
	want int
} {
	var out []struct {
		name string
		src  string
		want int
	}
	for _, tc := range nestedOptFlatCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, nestedOptChurn("", tc.body, 200), tc.want200})
	}
	for _, tc := range nestedOptHazardCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, tc.src, tc.want})
	}
	return out
}

// TestSelfHostNestedOptionReclaimArm64 runs them through the self-host arm64
// backend, which emits, assembles and links the finished binary itself.
func TestSelfHostNestedOptionReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedOptAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "nestedopt_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}

// TestSelfHostNestedOptionReclaimWasm is the wasm leg. Every `want` is well under
// WASI's 126 ceiling, so an over-release (+100) is still expressible.
func TestSelfHostNestedOptionReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-option wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedOptAllCases() {
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
			watFile := filepath.Join(dir, "nestedopt_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}
