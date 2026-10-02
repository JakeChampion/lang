package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// A branch on `target_os()` is a branch on a literal by the time codegen
// runs, and the IR fold prunes the dead arm: the artifact for one target
// holds only that target's arm — its string is in the emitted text, the
// other arm's is not. `-target arm64-linux` compiled on a Mac still says
// linux, because the value is the target's, so the darwin arm is the one
// that goes.
const targetOSBranchSrc = `import "core/int";
function main(): i32 {
    if (target_os() == "darwin") {
        print("darwin arm");
    } else if (target_os() != "wasi") {
        print("hosted arm");
    } else {
        print("wasi arm");
    }
    return 0;
}
`

func targetOSFront(t *testing.T, targetOS string) (*checker.Info, *ast.Program) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(targetOSBranchSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(src)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: targetOS}); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	return info, prog
}

// onlyArm fails unless the emitted artifact carries exactly the live arm's
// string and none of the dead arms'.
func onlyArm(t *testing.T, target, emitted, live string) {
	t.Helper()
	for _, arm := range []string{"darwin arm", "hosted arm", "wasi arm"} {
		has := strings.Contains(emitted, arm)
		if arm == live && !has {
			t.Errorf("%s: the live arm %q is missing from the emitted artifact", target, arm)
		}
		if arm != live && has {
			t.Errorf("%s: the dead arm %q survived into the emitted artifact", target, arm)
		}
	}
}

func TestTargetOSBranchKeepsOnlyTheLiveArm(t *testing.T) {
	t.Run("x86-64-linux", func(t *testing.T) {
		info, prog := targetOSFront(t, "linux")
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			t.Fatalf("x86_64 emit: %v", err)
		}
		onlyArm(t, "x86-64-linux", asm, "hosted arm")
	})
	t.Run("arm64-darwin", func(t *testing.T) {
		info, prog := targetOSFront(t, "darwin")
		asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
		if err != nil {
			t.Fatalf("arm64 emit: %v", err)
		}
		onlyArm(t, "arm64-darwin", asm, "darwin arm")
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		info, prog := targetOSFront(t, "wasi")
		core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{Preview2WASI: true, SynthCliRun: true})
		if err != nil {
			t.Fatalf("wasmbin.Build: %v", err)
		}
		onlyArm(t, "wasm32-wasi", string(core), "wasi arm")
	})
}

// A branch on target_os() is judged for the target it is built for: the
// dead arm's `read_file`, which the proxy world does not grant, reaches
// neither the capability gate (E066) nor the shake, so a check and a build
// for wasm32-wasi-http both pass. The self-host twin serves the handler
// (TestSelfHostWasiHttpTargetBranchGate).
func TestTargetOSBranchJudgedForTheTarget(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(src, []byte(`import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (target_os() == "wasi-http") {
        return http.ok("hosted arm");
    } else {
        match (read_file("/etc/hostname")) {
            Ok(text) => { return http.ok("dialled arm " + text); },
            Err(e) => { return http.ok("dialled arm"); }
        }
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-check", "-target", "wasm32-wasi-http", src).CombinedOutput(); err != nil {
		t.Fatalf("-check -target wasm32-wasi-http: %v\n%s", err, out)
	}
	out := filepath.Join(dir, "handler.wasm")
	if msg, err := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("-target wasm32-wasi-http: %v\n%s", err, msg)
	}
}

// An if-expression on a literal condition is a value: both arms stay for
// the lowering to read, while a statement `if` inside an arm is pruned to
// the arm it takes. Both compilers run it (the arm64 lane compiles with the
// self-host compiler too).
func TestIfExpressionOnLiteralKeepsItsArms(t *testing.T) {
	src := `function main(): i32 {
    var n: i32 = if (false) { 1 } else if (true) {
        var k: i32 = 0;
        if (true) { k = 7; } else { k = 8; }
        var j: i32 = if (false) { k } else { k + 1 };
        j
    } else { 4 };
    var m: i64 = if (true) { 1000000 as i64 * 1234567 as i64 } else { 0 };
    if (n == 8 && m == 1234567000000) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("if-expression chain on literals got %d, want 0", code)
	}
}
