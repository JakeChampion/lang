package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostTypedJSONProtobufErrors(t *testing.T) {
	checkJSONDecodeFixture(t, "typed_json_protobuf_errors")
}

func TestSelfHostJSONArraysAndOptions(t *testing.T) {
	checkJSONDecodeFixture(t, "json_arrays_and_options")
}

func checkJSONDecodeFixture(t *testing.T, name string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	path := langSrcAbs(t, "conformance/cases/"+name+"/main.fern")
	for name, cmd := range map[string]*exec.Cmd{
		"Go-check":    exec.Command(oracle, "-check", path),
		"Go-interp":   exec.Command(oracle, "-interp", path),
		"self-check":  runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
		"self-interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("typed errors: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("typed errors: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
