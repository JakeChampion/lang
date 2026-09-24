package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// The preview-1 helpers behind args(), environ() and env(name) read the host's
// vector into two $__fern_alloc scratch buffers (the pointer table and the
// string bytes) and copy out of them. None freed either buffer, so every call
// left two blocks live. environ() also had no instruction selection at all:
// kind 249 was missing from the host-op predicate, so the backend refused it.
//
// Each program is run with an empty environment and with two variables set,
// because an empty one makes both scratch requests zero bytes.
func TestSelfHostWasmHostScratchFreed(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm host scratch census")
	}
	compile := selfHostCLIWasmCompiler(t)

	cases := []struct {
		name, src string
		// exit with no environment, then with A=bc and BB=d.
		bare, withEnv int
	}{
		{"args", `function main(): i32 {
    var n: i32 = 0;
    var k: i32 = 0;
    while (k < 3) { var xs: string[] = args(); n = n + xs.len() + xs[1].len(); k = k + 1; }
    return n;
}`, 15, 15},
		{"environ", `function main(): i32 {
    var n: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var es: string[] = environ();
        for e in es { n = n + e.len(); }
        n = n + es.len() * 10;
        k = k + 1;
    }
    return n;
}`, 0, 84},
		{"env", `function main(): i32 {
    var n: i32 = 0;
    var k: i32 = 0;
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
			watFile := compile(t, mustWrite(t, t.TempDir(), "main.fern", tc.src), tc.name)
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
