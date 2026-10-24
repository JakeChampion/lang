package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type optAliasBindCase struct {
	name string
	src  string
	want int
}

const optAliasBindMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func optAliasBindCases() []optAliasBindCase {
	return []optAliasBindCase{
		{
			name: "arr_alias_never_read",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    return 7;
}` + optAliasBindMain,
			want: 36,
		},
		{
			name: "arr_no_alias",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    return 7;
}` + optAliasBindMain,
			want: 36,
		},
		{
			name: "arr_alias_matched",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    match (x) { Some(a) => { t = a.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 34,
		},
		{
			name: "arr_both_matched",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    match (x) { Some(a) => { t = a.len(); }, None => {} }
    match (src) { Some(b) => { t = t + b.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 68,
		},
		{
			name: "arr_source_matched",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    match (src) { Some(b) => { t = b.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 34,
		},
		{
			name: "str_alias_never_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: Option[string] = Some(w("ab"));
    let x: Option[string] = src;
    return 7;
}` + optAliasBindMain,
			want: 36,
		},
		{
			name: "str_alias_matched",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let src: Option[string] = Some(w("ab"));
    let x: Option[string] = src;
    match (x) { Some(s) => { t = s.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 51,
		},
		{
			name: "optopt_alias_never_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: Option[Option[string]] = Some(Some(w("ab")));
    let x: Option[Option[string]] = src;
    return 7;
}` + optAliasBindMain,
			want: 36,
		},
		{
			name: "arr_conditional_alias",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) {
        let x: Option[i32[]] = src;
        return 5;
    }
    return 7;
}` + optAliasBindMain,
			want: 19,
		},
		{
			name: "arr_alias_chain",
			src: `function round(i: i32): i32 {
    let t: Option[i32[]] = Some([i, i + 1]);
    let v: Option[i32[]] = t;
    let u: Option[i32[]] = v;
    return i % 7;
}` + optAliasBindMain,
			want: 46,
		},
		{
			name: "arr_alias_chain_three_links",
			src: `function round(i: i32): i32 {
    let t: Option[i32[]] = Some([i, i + 1]);
    let v: Option[i32[]] = t;
    let u: Option[i32[]] = v;
    let z: Option[i32[]] = u;
    return i % 7;
}` + optAliasBindMain,
			want: 46,
		},
		{
			name: "arr_alias_chain_matched",
			src: `function round(i: i32): i32 {
    let t: Option[i32[]] = Some([i, i + 1]);
    let v: Option[i32[]] = t;
    let u: Option[i32[]] = v;
    let n: i32 = 0;
    match (u) { Some(a) => { n = a.len(); }, None => {} }
    return n + i;
}` + optAliasBindMain,
			want: 4,
		},
		{
			name: "str_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: Option[string] = Some(w("ab"));
    let v: Option[string] = t;
    let u: Option[string] = v;
    return i % 7;
}` + optAliasBindMain,
			want: 46,
		},
		{
			name: "optopt_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: Option[Option[string]] = Some(Some(w("ab")));
    let v: Option[Option[string]] = t;
    let u: Option[Option[string]] = v;
    return i % 7;
}` + optAliasBindMain,
			want: 46,
		},
		{
			name: "arr_alias_chain_conditional",
			src: `function round(i: i32): i32 {
    let t: Option[i32[]] = Some([i, i + 1]);
    let n: i32 = 0;
    if (i % 2 == 0) {
        let v: Option[i32[]] = t;
        let u: Option[i32[]] = v;
        n = 1;
    }
    return n + i;
}` + optAliasBindMain,
			want: 20,
		},
		{
			name: "refuses_alias_chain_payload_out",
			src: `function round(i: i32): i32 {
    let t: Option[i32[]] = Some([i, i + 1]);
    let v: Option[i32[]] = t;
    let u: Option[i32[]] = v;
    let out: i32[] = [0];
    match (u) { Some(a) => { out = a; }, None => {} }
    return out.len() + i;
}` + optAliasBindMain,
			want: 4,
		},
		{
			name: "refuses_alias_chain_returned",
			src: `function esc(i: i32): Option[i32[]] {
    let t: Option[i32[]] = Some([i, i + 1]);
    let v: Option[i32[]] = t;
    let u: Option[i32[]] = v;
    return u;
}
function round(i: i32): i32 {
    let n: i32 = 0;
    match (esc(i)) { Some(a) => { n = a.len(); }, None => {} }
    return n + i;
}` + optAliasBindMain,
			want: 4,
		},
		{
			name: "refuses_alias_carrying_payload_out",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let out: i32[] = [0];
    match (x) { Some(xs) => { out = xs; }, None => {} }
    return out.len();
}` + optAliasBindMain,
			want: 34,
		},
		{
			name: "refuses_reassigned_alias",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    x = None;
    return 7;
}` + optAliasBindMain,
			want: 36,
		},
		{
			name: "arr_alias_in_a_loop",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let src: Option[i32[]] = Some([i, i + j]);
        let x: Option[i32[]] = src;
        match (x) { Some(a) => { t = (t + a.len()) % 101; }, None => {} }
        j = j + 1;
    }
    return t;
}` + optAliasBindMain,
			want: 19,
		},
	}
}

func TestSelfHostOptAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaliasbind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the shared box "+
					"decremented once more than it was retained, or the payload "+
					"released by both owners)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A "+
					"short free count is the alias bind denying the source its "+
					"credit again", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "optaliasbind_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

func TestSelfHostOptAliasBindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Option alias-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
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
			watFile := filepath.Join(dir, "optaliasbind_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("Option alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostOptAliasBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optaliasbind_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("Option alias-bind arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
