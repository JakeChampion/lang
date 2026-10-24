package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const chdirSource = `
// Naming every variant forces chdir's Err payload to BE the IoError enum.
// Matching Err(e) and then dropping e does not, which is how the builtin
// shipped with its error declared as a STRUCT of that name: unusable by
// anything taking an IoError, and invisible to a test that never looks inside
// the error it was handed.
function code_of(e: IoError): i32 {
    match (e) {
        NotFound(_) => { return 1; },
        PermissionDenied(_) => { return 2; },
        AlreadyExists(_) => { return 3; },
        InvalidUtf8(_) => { return 4; },
        Interrupted => { return 5; },
        Unsupported => { return 6; },
        Other(c, m, _) => { return 7; }
    }
}

function main(): i32 {
    match (stat("dev")) {
        Ok(v) => { return 61; },
        Err(e) => {}
    }
    match (chdir("/")) {
        Ok(v) => {},
        Err(e) => { return 62; }
    }
    match (stat("dev")) {
        Ok(v) => {},
        Err(e) => { return 63; }
    }
    // A path that does not exist is an error, not a silent no-op...
    match (chdir("/no-such-directory-at-all-9090")) {
        Ok(v) => { return 64; },
        Err(e) => {
            if (code_of(e) != 1) { return 68; }
        }
    }
    // ...and a failed move leaves the process where it was.
    match (stat("dev")) {
        Ok(v) => {},
        Err(e) => { return 65; }
    }
    // A regular file is ENOTDIR rather than success, which the IoError carries
    // as Other since there is no named variant for it.
    match (chdir("/dev/null")) {
        Ok(v) => { return 66; },
        Err(e) => {
            if (code_of(e) != 7) { return 69; }
        }
    }
    match (stat("dev")) {
        Ok(v) => {},
        Err(e) => { return 67; }
    }
    return 0;
}`

const signalMaskSource = `
function main(): i32 {
    let bit: i64 = 2 as i64;
    // Start from a known state rather than assuming one.
    signal_mask(1, bit);
    if ((signal_mask(0, 0 as i64) & bit) != (0 as i64)) { return 61; }
    // Blocking answers the mask as it was: without the bit.
    let prev: i64 = signal_mask(0, bit);
    if ((prev & bit) != (0 as i64)) { return 62; }
    if ((signal_mask(0, 0 as i64) & bit) == (0 as i64)) { return 63; }
    // Unblocking answers the mask as it was: WITH the bit.
    let p2: i64 = signal_mask(1, bit);
    if ((p2 & bit) == (0 as i64)) { return 64; }
    if ((signal_mask(0, 0 as i64) & bit) != (0 as i64)) { return 65; }
    // Replace outright rather than adding to or removing from.
    signal_mask(2, bit);
    if ((signal_mask(0, 0 as i64) & bit) == (0 as i64)) { return 66; }
    signal_mask(2, 0 as i64);
    if (signal_mask(0, 0 as i64) != (0 as i64)) { return 67; }
    return 0;
}`

const signalDispositionSource = `
function main(): i32 {
    if (signal_default(2) != 0) { return 61; }
    if (signal_disposition(2) != 0) { return 62; }
    if (signal_ignore(2) != 0) { return 63; }
    if (signal_disposition(2) != 1) { return 64; }
    if (signal_default(2) != 0) { return 65; }
    if (signal_disposition(2) != 0) { return 66; }
    // SIGKILL cannot be caught or ignored: EINVAL, not a silent success.
    if (signal_ignore(9) >= 0) { return 67; }
    if (signal_default(9) >= 0) { return 68; }
    return 0;
}`

func runChdirSignalChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  string
	}{
		{"chdir", chdirSource},
		{"signal-mask", signalMaskSource},
		{"signal-disposition", signalDispositionSource},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := run(t, c.src); got != 0 {
				t.Fatalf("exit %d, want 0 — the code names the step (see the source above)", got)
			}
		})
	}
}

func TestX86_64ChdirSignalMask(t *testing.T) {
	runChdirSignalChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunX86_64(t, src)
		return exit
	})
}

func TestArm64ChdirSignalMask(t *testing.T) {
	runChdirSignalChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunArm64(t, src)
		return exit
	})
}

func TestArm64DarwinChdirSignalMask(t *testing.T) {
	bin := buildFernCLI(t)
	runChdirSignalChecks(t, func(t *testing.T, src string) int {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out := filepath.Join(dir, "prog")
		if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
		}
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			t.Skip("execution check only runs on Apple Silicon")
		}
		cmd := exec.Command(out)
		cmd.Dir = dir
		_ = cmd.Run()
		ps := cmd.ProcessState
		if ps == nil || !ps.Exited() {
			t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
		}
		return ps.ExitCode()
	})
}

func TestInterpChdirSignalMask(t *testing.T) {
	runChdirSignalChecks(t, func(t *testing.T, src string) int {
		return runInterpExit(t, src)
	})
}

func TestWASMChdirRefused(t *testing.T) {
	src := `
function main(): i32 {
    match (chdir("/")) {
        Ok(v) => { return 0; },
        Err(e) => { return 1; }
    }
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted chdir; it has no process working directory", target)
			continue
		}
		if vs[0].Builtin != "chdir" || vs[0].Capability != "cwd" {
			t.Errorf("%s refused %q on %q, want chdir on cwd", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
