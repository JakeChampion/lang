package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A branch on `target_os()` is a branch on a literal by the time codegen
// runs, and the fold prunes the dead arm: the artifact for one target
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

func targetOSSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(targetOSBranchSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return src
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
	cli := e2eharness.SelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		asm := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, targetOSSource(t))
		onlyArm(t, "x86-64-linux", asm, "hosted arm")
	})
	t.Run("arm64-darwin", func(t *testing.T) {
		asm := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetArm64Darwin, targetOSSource(t))
		onlyArm(t, "arm64-darwin", asm, "darwin arm")
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		core, err := os.ReadFile(e2eharness.CompileSelfHostFile(t, e2eharness.TargetWasm32Wasi, targetOSSource(t), nil))
		if err != nil {
			t.Fatal(err)
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
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
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
    let n: i32 = if (false) { 1 } else if (true) {
        let k: i32 = 0;
        if (true) { k = 7; } else { k = 8; }
        let j: i32 = if (false) { k } else { k + 1 };
        j
    } else { 4 };
    let m: i64 = if (true) { 1000000 as i64 * 1234567 as i64 } else { 0 };
    if (n == 8 && m == 1234567000000) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("if-expression chain on literals got %d, want 0", code)
	}
}
