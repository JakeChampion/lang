package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostWasmHostScratchFreed(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm host scratch census")
	}
	cli := buildSelfHostCLI(t)

	cases := []struct {
		name, src     string
		bare, withEnv int
	}{
		{"zero-allocation", `function main(): i32 {
    unsafe { let p: usize = __alloc(0); __free(p, 0); }
    return 0;
}`, 0, 0},
		{"args", `function main(): i32 {
    let n: i32 = 0;
    let k: i32 = 0;
    while (k < 3) { let xs: string[] = args(); n = n + xs.len() + xs[1].len(); k = k + 1; }
    return n;
}`, 15, 15},
		{"environ", `function main(): i32 {
    let n: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let es: string[] = environ();
        for e in es { n = n + e.len(); }
        n = n + es.len() * 10;
        k = k + 1;
    }
    return n;
}`, 0, 84},
		{"env", `function main(): i32 {
    let n: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        match (env("BB")) { Some(v) => { n = n + v.len() + 1; }, None => { n = n + 10; } }
        match (env("ZZ")) { Some(v) => { n = n + 100; }, None => { n = n + 1; } }
        k = k + 1;
    }
    return n;
}`, 33, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			watFile := cli.emit(t, mustWrite(t, t.TempDir(), "main.fern", tc.src), "wasm32-wasi", "FERN_LEAKCHECK=1")
			for _, leg := range []struct {
				label string
				env   []string
				want  int
			}{
				{"bare", nil, tc.bare},
				{"with-env", []string{"--env", "A=bc", "--env", "BB=d"}, tc.withEnv},
			} {
				args := append(append([]string{"run"}, leg.env...), watFile, "xyz")
				cmd := exec.Command("wasmtime", args...)
				var eb bytes.Buffer
				cmd.Stderr = &eb
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != leg.want {
					t.Fatalf("%s: exit = %d, want %d\n%s", leg.label, code, leg.want, eb.String())
				}
				allocs, frees, live := leakSummaryOf(t, tc.name+"/"+leg.label, eb.String())
				if allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("%s: allocs=%d frees=%d live_bytes=%d, want balanced", leg.label, allocs, frees, live)
				}
			}
		})
	}
}

func TestSelfHostWasmRawFreeHeapCursor(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name, release string
	}{
		{"nonempty-at-cursor", "__free(p, 8);"},
		{"empty-above-cursor", "__free(p + 8 as usize, 0);"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { unsafe { let p: usize = __alloc(0); " + tc.release + " } return 0; }"
			wat := cli.emit(t, mustWrite(t, t.TempDir(), "main.fern", src), "wasm32-wasi")
			cmd := exec.Command("wasmtime", "run", wat)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState.ExitCode() != 134 || !strings.HasPrefix(string(out), "fern: release of memory outside the heap\n") {
				t.Fatalf("invalid release: %v\n%s", err, out)
			}
		})
	}
}
