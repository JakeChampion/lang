package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const floatLabelNSProg = `import "std/f64";
function main(): i32 {
    let a: f64 = 1.0 / 3.0;
    let b: f64 = 0.1;
    write(a.to_string() + "|" + b.to_string() + "|" + a.to_string_prec(4) + "\n");
    return 0;
}
`

var labelDef = regexp.MustCompile(`(?m)^(\.[A-Za-z_][A-Za-z0-9_$]*):`)

func TestSelfHostPerModuleLabelNS_X86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	interpBin := buildLangBinForInterp(t)

	progDir := t.TempDir()
	copyStdlibTree(t, progDir)
	entry := filepath.Join(progDir, "main.fern")
	if err := os.WriteFile(entry, []byte(floatLabelNSProg), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	asm := string(runDriverFile(t, runner, driverBin, entry))
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes")
	}

	if !strings.Contains(asm, ".S__entry_") {
		t.Fatal("no namespaced labels: the program no longer routes per-module (import set changed?) — pick one that does")
	}

	var dups []string
	seen := map[string]bool{}
	for _, m := range labelDef.FindAllStringSubmatch(asm, -1) {
		if seen[m[1]] {
			dups = append(dups, m[1])
			continue
		}
		seen[m[1]] = true
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		if len(dups) > 8 {
			dups = dups[:8]
		}
		t.Errorf("duplicate label definitions across per-module units: %v", dups)
	}

	want, err := exec.Command(interpBin, "-interp", entry).Output()
	if err != nil {
		t.Fatalf("interp oracle: %v", err)
	}
	progBin := buildBin(t, gcc, progDir, "labelns", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	got, rerr := cmd.Output()
	if rerr != nil {
		t.Fatalf("run: %v", rerr)
	}
	if string(got) != string(want) {
		t.Errorf("stdout = %q, want %q (interp oracle)", got, want)
	}
}
