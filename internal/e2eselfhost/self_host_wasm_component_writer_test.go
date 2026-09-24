package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A Writer's write builds as a wasm component (#10206). stdout() and stderr()
// are fds 1 and 2, so the write goes through the same preview2 stream shim
// print and eprint use. A stderr() Writer selects the stderr framing on its
// own, with no eprint in the program. Each program must match native's
// component on stdout, stderr and exit code.
func TestSelfHostWasmComponentWriter(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping component writer e2e")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	cases := []struct{ name, src string }{
		{"stdout", `function main(): i32 {
    var w: Writer = stdout();
    match (w.write("ok\n")) { Some(_) => { return 3; }, None => {} }
    return 0;
}
`},
		{"stderr", `function main(): i32 {
    var w: Writer = stderr();
    w.write("err\n");
    return 7;
}
`},
		{"mixed", `function main(): i32 {
    var w: Writer = stdout();
    w.write("one\n");
    print("two");
    var e: Writer = stderr();
    e.write("three\n");
    return 0;
}
`},
	}
	run := func(t *testing.T, wasm string) (string, string, int) {
		t.Helper()
		cmd := exec.Command("wasmtime", "run", wasm)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			ref := filepath.Join(dir, tc.name+".native.wasm")
			if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", ref, src).CombinedOutput(); err != nil {
				t.Fatalf("native component: %v\n%s", err, out)
			}
			got := filepath.Join(dir, tc.name+".wasm")
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", src, cli.stdlib, "-o", got)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host component: %v\n%s", err, out)
			}
			wo, we, wc := run(t, ref)
			go_, ge, gc := run(t, got)
			if go_ != wo || ge != we || gc != wc {
				t.Fatalf("self-host stdout %q stderr %q exit %d; native stdout %q stderr %q exit %d", go_, ge, gc, wo, we, wc)
			}
		})
	}
}
