// `w.splice_forever(block)` where the kernel cannot take the block: the
// interpreter, both WASI previews and XNU refuse with Unsupported, which
// promises nothing reached the writer, so the caller writes the block itself.
// The Linux backends' vmsplice path is driven through a pipe in
// e2ecompiler's TestSelfHostSpliceForever.
package e2e

import "testing"

// spliceForeverRefusedSource exits 0 when the call answers Unsupported, and
// 1 for any other error or 2 for None, which the method never returns.
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

// XNU has no vmsplice. Built everywhere, run on Apple Silicon.
func TestArm64DarwinSpliceForeverUnsupported(t *testing.T) {
	buildAndRunDarwin(t, t.TempDir(), spliceForeverRefusedSource)
}
