package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// random_bytes gives back the scratch getrandom writes into (#9478): the
// helper frees it through the string box it wraps the scratch in, so a bound
// result balances on both lowerings, fifty calls in a loop.
const randomBytesScratchSrc = `function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 50) { var b: u8[] = random_bytes(16); t = t + b.len(); i = i + 1; }
    return t % 200;
}
`

func TestSelfHostRandomBytesScratch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "random_bytes_scratch.fern")
	if err := os.WriteFile(src, []byte(randomBytesScratchSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, lw := range []struct{ name, env string }{{"semantic", "FERN_SEM_IR=1"}, {"ast", "FERN_SEM_IR="}} {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 0 {
				t.Fatalf("exit=%d, want 0 (stderr %q)", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
