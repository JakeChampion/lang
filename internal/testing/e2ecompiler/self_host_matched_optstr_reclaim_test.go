package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type matchedOptstrCase struct {
	name string
	src  string
	want int
}

const matchedOptstrMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const matchedOptstrW = "function w(a: string): string { return a + \"!\"; }\n"

func matchedOptstrCases() []matchedOptstrCase {
	return []matchedOptstrCase{
		{
			name: "matched_payload_read",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "matched_binding_unused",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			name: "matched_wildcard",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			name: "unmatched_control",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    return i % 7;
}` + matchedOptstrMain,
			want: 13,
		},
		{
			name: "arr_matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 63,
		},
		{
			name: "arrarr_matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			name: "reassigned_all_rebinds_fresh",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "reassigned_array_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { o = Some([i + 2, i + 3]); }
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 14,
		},
		{
			name: "refuses_rebind_aliasing_param",
			src: matchedOptstrW + `function run(p: string, i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(p); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { let s: string = w("zz"); return run(s, i); }` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "refuses_reassigned_escaping",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let held: string = "";
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "refuses_returned_payload",
			src: matchedOptstrW + `function mk(i: i32): string {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v; }, None => { return "z"; } }
    return "y";
}
function round(i: i32): i32 { let s: string = mk(i); return s.len(); }` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "refuses_stored_outer",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let held: string = "";
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "refuses_passed_to_callee",
			src: matchedOptstrW + `function take(s: string): i32 { return s.len(); }
function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return take(v); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			name: "refuses_into_container",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let keep: string[] = [];
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { keep = keep.append(v); }, None => {} }
    return keep.len();
}` + matchedOptstrMain,
			want: 34,
		},
		{
			name: "refuses_concat_conservative",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { let t: string = v + "z"; return t.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 53,
		},
		{
			name: "refuses_aliased_param_payload",
			src: matchedOptstrW + `function wrap(p: string, i: i32): i32 {
    let o: Option[string] = Some(p);
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { let s: string = w("ab"); return wrap(s, i); }` + matchedOptstrMain,
			want: 19,
		},
	}
}

func TestSelfHostMatchedOptstrReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "matchoptstr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a payload the "+
					"arm handed out was released under a live reference)", tc.name, exit, tc.want)
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
		})
	}
}

func TestSelfHostMatchedOptstrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping matched Option[string] wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "matchoptstr_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("matched Option[string] wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostMatchedOptstrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "matchoptstr_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("matched Option[string] arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
