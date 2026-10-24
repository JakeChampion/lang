package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelfHostInterpHostedBuiltinsMatchOracle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the programs name Linux paths and errnos")
	}
	cli := buildSelfHostCLI(t)
	oracle := buildFernCLIBin(t)
	files, err := filepath.Glob(filepath.Join("testdata", "interp_hosted", "*.fern"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs under testdata/interp_hosted: %v", err)
	}
	for _, prog := range files {
		prog, _ := filepath.Abs(prog)
		t.Run(strings.TrimSuffix(filepath.Base(prog), ".fern"), func(t *testing.T) {
			oDir, sDir := t.TempDir(), t.TempDir()
			oCmd := exec.Command(oracle, "-interp", prog, oDir)
			sCmd := runX86_64Bin(cli.runner, cli.bin, "-interp", prog, cli.stdlib, sDir)
			for _, c := range []*exec.Cmd{oCmd, sCmd} {
				c.Env = append(os.Environ(), "FERN_HOSTED_PROBE=yes")
			}
			oOut, oErr, oCode := runInterpCLI(t, oCmd, "")
			sOut, sErr, sCode := runInterpCLI(t, sCmd, "")
			if sOut != oOut || sErr != oErr || sCode != oCode {
				t.Errorf("fern -interp and fern-oracle -interp disagree\n"+
					"fern -interp:        exit %d\nstdout:\n%s\nstderr:\n%s\n"+
					"fern-oracle -interp: exit %d\nstdout:\n%s\nstderr:\n%s",
					sCode, sOut, sErr, oCode, oOut, oErr)
			}
			if strings.Contains(sErr, "fern: interp:") {
				t.Errorf("the self-host interpreter could not evaluate the program:\n%s", sErr)
			}
		})
	}
}

func TestSelfHostInterpRefusesTheRawFloor(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		call       string
		code       int
		diagnostic string
	}{
		{"__syscall3(39, 0i64, 0i64, 0i64) as i32", 1, `error[E001]: cannot find "__syscall3" in this scope`},
		{"__c_call0(0 as usize) as i32", 254, "takes machine addresses, and the interpreter has no address space"},
	} {
		call := tc.call
		t.Run(strings.Fields(call)[0], func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "raw.fern")
			if err := os.WriteFile(src, []byte("function main(): i32 { return "+call+"; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, stderr, code := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib), "")
			if code != tc.code || !strings.Contains(stderr, tc.diagnostic) {
				t.Errorf("exit %d, stderr %q; want %d and %q", code, stderr, tc.code, tc.diagnostic)
			}
		})
	}
}
