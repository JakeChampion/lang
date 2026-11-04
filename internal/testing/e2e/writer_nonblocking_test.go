package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// The writer fills a pipe that nobody reads. If the flag does not reach the
// descriptor, the writer waits here and the test timeout stops it.
const writerNonblockingSource = `import "std/string";

function fill(w: Writer): i32 {
    let chunk: string = "x".repeat(4096);
    let total: i64 = 0;
    let round: i32 = 0;
    while (round < 1024) {
        match (w.write_some(chunk)) {
            Ok(n) => { total = total + n; },
            Err(_) => {
                if (total < 4096) {
                    return 3;
                }
                return 0;
            }
        }
        round = round + 1;
    }
    return 4;
}

function main(): i32 {
    match (pipe()) {
        Ok(q) => {
            match (q.w.set_nonblocking(true)) { Ok(_) => {}, Err(_) => { return 2; } }
            let code: i32 = fill(q.w);
            if (code != 0) {
                return code;
            }
            match (q.w.set_nonblocking(false)) { Ok(_) => {}, Err(_) => { return 5; } }
            q.r.close();
            q.w.close();
        },
        Err(_) => { return 1; }
    }
    print("filled");
    return 0;
}
`

// No wasm world can change the flag of a descriptor, so the call answers an
// error there.
const writerNonblockingWasmSource = `function main(): i32 {
    match (open_writer("out.txt")) {
        Ok(w) => {
            match (w.set_nonblocking(true)) { Ok(_) => { return 21; }, Err(_) => {} }
            w.close();
        },
        Err(_) => { return 22; }
    }
    return 0;
}
`

func TestX86_64WriterSetNonblocking(t *testing.T) {
	if code, _ := compileRunX86_64WithSetup(t, writerNonblockingSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (the exit code names the step)", code)
	}
}

func TestArm64WriterSetNonblocking(t *testing.T) {
	out, code := compileAndRunArm64(t, writerNonblockingSource)
	if code != 0 || out != "filled\n" {
		t.Fatalf("exit = %d, stdout = %q; want 0 and %q (the exit code names the step)", code, out, "filled\n")
	}
}

func TestInterpWriterSetNonblocking(t *testing.T) {
	if code := runInterpExit(t, writerNonblockingSource); code != 0 {
		t.Fatalf("exit = %d, want 0 (the exit code names the step)", code)
	}
}

func TestWASMPreview1WriterSetNonblockingUnsupported(t *testing.T) {
	mod := buildWasmCore(t, writerNonblockingWasmSource)
	if got := runPreview1Module(t, mod, t.TempDir()); got != 0 {
		t.Fatalf("main = %d, want 0 (the code names the step)", got)
	}
}

func TestWASMWriterSetNonblockingUnsupported(t *testing.T) {
	out := runResultStdout(t, writerNonblockingWasmSource, runOpts{workDir: t.TempDir()})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 (the code names the step)\nstdout:\n%s", got, out)
	}
}

func TestWriterSetNonblockingNotCapabilityGated(t *testing.T) {
	prog, err := parser.Parse(writerNonblockingWasmSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}
