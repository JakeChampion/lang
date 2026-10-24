package e2e

import "testing"

const spliceForeverRefusedSource = `function main(): i32 {
    match (stdout().splice_forever("y\n")) {
        Err(e) => {
            match (e) {
                Unsupported => { return 0; },
                _ => { return 1; }
            }
        },
        Ok(_) => { return 2; }
    }
}
`

func TestInterpSpliceForeverUnsupported(t *testing.T) {
	if code := runInterpExit(t, spliceForeverRefusedSource); code != 0 {
		t.Fatalf("exit = %d, want 0 (Unsupported)", code)
	}
}

func TestWASMPreview1SpliceForeverUnsupported(t *testing.T) {
	mod := buildWasmCore(t, spliceForeverRefusedSource)
	if got := runPreview1Module(t, mod, t.TempDir()); got != 0 {
		t.Fatalf("main = %d, want 0 (Unsupported)", got)
	}
}

func TestWASMSpliceForeverUnsupported(t *testing.T) {
	out := runResultStdout(t, spliceForeverRefusedSource, runOpts{workDir: t.TempDir()})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 (Unsupported)", got)
	}
}

func TestArm64DarwinSpliceForeverUnsupported(t *testing.T) {
	buildAndRunDarwin(t, t.TempDir(), spliceForeverRefusedSource)
}
