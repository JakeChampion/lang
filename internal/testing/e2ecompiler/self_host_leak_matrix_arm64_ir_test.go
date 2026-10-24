// CI-DARK: FERN_LEAK_MATRIX_DUMP — This flag regenerates the reference data.
package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func nativeLeakVerdictArm64(t *testing.T, cli, qemu, dir, name, src string) (leakVerdict, int) {
	t.Helper()
	srcPath := filepath.Join(dir, name+"_a64.fern")
	binPath := filepath.Join(dir, name+"_a64.nat")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	compile := exec.Command(cli, "-target", "arm64-linux", "-o", binPath, srcPath)
	compile.Env = childEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Logf("%s: native arm64 compile failed:\n%s", name, out)
		return verdictError, -1
	}
	cmd := runArm64Bin(qemu, binPath)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	exit := cmd.ProcessState.ExitCode()
	if exit == -1 || !cmd.ProcessState.Exited() {
		return verdictCrash, exit
	}
	return verdictFromLeakcheck(t, name+" (native arm64)", errBuf.String()), exit
}

func selfHostLeakVerdictArm64(t *testing.T, arm64gcc, qemu string, x86runner []string, driverBin, dir, name, src string) (leakVerdict, int) {
	t.Helper()
	asm, stderr, err := loadCompile(t, x86runner, driverBin, src, []string{"FERN_LEAKCHECK=1"}, "-target", "arm64-linux")
	if err != nil {
		t.Logf("%s: self-host arm64 compile refused: %v\n%s", name, err, stderr)
		return verdictError, -1
	}
	bin := buildBinArm64(t, arm64gcc, dir, "leakmxa64_"+name, asm)
	rcmd := runArm64Bin(qemu, bin)
	var errBuf strings.Builder
	rcmd.Stderr = &errBuf
	_ = rcmd.Run()
	exit := rcmd.ProcessState.ExitCode()
	if exit == -1 || exit == 139 || exit == 134 || exit == 137 {
		return verdictCrash, exit
	}
	return verdictFromLeakcheck(t, name+" (self-host arm64)", errBuf.String()), exit
}

func loadLeakMatrixArm64(t *testing.T) map[string][2]leakVerdict {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-leak-matrix-arm64.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string][2]leakVerdict{}
	for ln, line := range strings.Split(readAll(t, f), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s:%d: want `<cell> <native> <selfhost> <note>`, got %q", path, ln+1, line)
		}
		out[fields[0]] = [2]leakVerdict{leakVerdict(fields[1]), leakVerdict(fields[2])}
	}
	return out
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read %s: %v", f.Name(), err)
	}
	return string(b)
}

func TestSelfHostLeakMatrixIRArm64(t *testing.T) {
	dump := envflag.On("FERN_LEAK_MATRIX_DUMP")
	var known map[string][2]leakVerdict
	if !dump {
		known = loadLeakMatrixArm64(t)
	}

	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "driver")

	cells := leakMatrixCells()
	seen := map[string]bool{}
	for _, cell := range cells {
		seen[cell.name] = true
		t.Run(cell.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdictArm64(t, cli, qemu, dir, cell.name, cell.src)
			shV, shExit := selfHostLeakVerdictArm64(t, arm64gcc, qemu, x86runner, driverBin, dir, cell.name, cell.src)

			if dump {
				fmt.Printf("%-45s %-6s %-6s (exit native=%d selfhost=%d)\n",
					cell.name, natV, shV, natExit, shExit)
				return
			}

			if natExit == 99 || shExit == 99 {
				t.Fatalf("underflow guard tripped (native=%d self-host=%d): an "+
					"over-release, which no matrix entry may pin", natExit, shExit)
			}
			if shV == verdictCrash {
				t.Errorf("self-host arm64 binary crashed (exit %d) — file it, do not pin it:\n%s", shExit, cell.src)
				return
			}
			if natV != verdictError && shV != verdictError && natExit != shExit {
				t.Errorf("exit codes disagree: native=%d self-host=%d — a wrong-code "+
					"divergence, not a leak-matrix update:\n%s", natExit, shExit, cell.src)
				return
			}

			rec, listed := known[cell.name]
			if !listed {
				t.Errorf("cell not in testdata/selfhost-leak-matrix-arm64.txt (measured "+
					"native=%s selfhost=%s). Rerun with FERN_LEAK_MATRIX_DUMP=1 and add "+
					"the line with a note naming the issue or reason", natV, shV)
				return
			}
			if rec[0] != natV || rec[1] != shV {
				t.Errorf("verdict moved: recorded native=%s selfhost=%s, measured "+
					"native=%s selfhost=%s. A leak→clean move is progress — update the "+
					"row (and its note) in the same change that caused it; clean→leak "+
					"is a regression", rec[0], rec[1], natV, shV)
			}
		})
	}

	if !dump {
		for name := range known {
			if !seen[name] {
				t.Errorf("testdata pins %q but the generator emits no such cell — "+
					"rename or remove the row", name)
			}
		}
	}
}
