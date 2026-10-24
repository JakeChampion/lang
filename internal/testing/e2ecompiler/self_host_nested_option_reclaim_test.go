package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func nestedOptChurn(prelude, body string, rounds int) string {
	return fmt.Sprintf(`%sfunction churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
%s
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, prelude, body, rounds)
}

var nestedOptFlatCases = []struct {
	name    string
	body    string
	want100 int
	want200 int
}{
	{
		name: "option_inner",
		body: `        let o: Option[Option[i32]] = Some(Some(i));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		name: "result_inner",
		body: `        let o: Option[Result[i32, i32]] = Some(Ok(i));
        match (o) {
            Some(inner) => { match (inner) { Ok(v) => { acc = (acc + v) % 91; }, Err(e) => { acc = (acc + e) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		name: "unused_binding",
		body: `        let o: Option[Option[i32]] = Some(Some(i));
        match (o) { Some(_) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`,
		want100: 9,
		want200: 18,
	},
	{
		name: "rc_inner_array_literal",
		body: `        let o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len() + v[0]) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 54,
		want200: 7,
	},
	{
		name: "rc_inner_array_ident",
		body: `        let xs: i32[] = [i, i + 1];
        let o: Option[Option[i32[]]] = Some(Some(xs));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
	{
		name: "rc_inner_string",
		body: `        let o: Option[Option[string]] = Some(Some("ab"));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
	{
		name: "rc_inner_string_array",
		body: `        let o: Option[Option[string[]]] = Some(Some(["a", "b"]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`,
		want100: 18,
		want200: 36,
	},
}

var nestedOptHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "binding_escapes",
		src: nestedOptChurn("", `        let keep: Option[i32] = None;
        let o: Option[Option[i32]] = Some(Some(i));
        match (o) { Some(inner) => { keep = inner; }, None => { acc = (acc + 2) % 91; } }
        match (keep) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 3) % 91; } }`, 200),
		want: 62,
	},
	{
		name: "inner_is_local",
		src: nestedOptChurn("", `        let inner0: Option[i32] = Some(i);
        let o: Option[Option[i32]] = Some(inner0);
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`, 200),
		want: 62,
	},
	{
		name: "bound_from_call",
		src: nestedOptChurn("function mk(i: i32): Option[Option[i32]] { let q: Option[Option[i32]] = Some(Some(i)); return q; }\n",
			`        let o: Option[Option[i32]] = mk(i);
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { acc = (acc + v) % 91; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }`, 200),
		want: 62,
	},
	{
		name: "inner_payload_escapes",
		src: nestedOptChurn("", `        let held: i32[] = [];
        let o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        match (o) {
            Some(inner) => { match (inner) { Some(v) => { held = v; }, None => { acc = (acc + 1) % 91; } } },
            None => { acc = (acc + 2) % 91; }
        }
        acc = (acc + held.len()) % 91;`, 200),
		want: 36,
	},
	{
		name: "inner_none_under_rc_type",
		src: nestedOptChurn("", `        let o: Option[Option[i32[]]] = Some(None);
        match (o) { Some(inner) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`, 200),
		want: 18,
	},
	{
		name: "triple_nested",
		src: nestedOptChurn("", `        let o: Option[Option[Option[i32]]] = Some(Some(Some(i)));
        match (o) { Some(inner) => { acc = (acc + 1) % 91; }, None => { acc = (acc + 2) % 91; } }`, 200),
		want: 18,
	},
	{
		name: "inner_box_extracted",
		src: nestedOptChurn("", `        let o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
        let keep: Option[i32[]] = None;
        match (o) { Some(inner) => { keep = inner; }, None => { acc = (acc + 2) % 91; } }
        match (keep) { Some(v) => { acc = (acc + v.len()) % 91; }, None => { acc = (acc + 3) % 91; } }`, 200),
		want: 36,
	},
}

func TestSelfHostNestedOptionReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostNestedOptionHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostNestedOptionReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostNestedOptionReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-option wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedOptAllCases() {
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
