package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

var inferredReuseModules = []string{
	"lexer.fern",
	"checker.fern",
	"ssarc.fern",
	"ssaunits.fern",
	"semsource.fern",
}

func TestSelfHostInferredReuseIsIdentical(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	selfHostRoot, err := filepath.Abs("../../../compiler")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, mod := range inferredReuseModules {
		t.Run(mod, func(t *testing.T) {
			src := filepath.Join(selfHostRoot, mod)
			reused := emitSelfHostAsm(t, runner, fernBin, stdlibRoot, src, "reused", nil)
			relowered := emitSelfHostAsm(t, runner, fernBin, stdlibRoot, src, "relowered", []string{"FERN_SEM_REUSE="})
			if len(reused) == 0 {
				t.Fatalf("%s: the reuse pass emitted nothing, so this comparison proves nothing", mod)
			}
			if len(relowered) == 0 {
				t.Fatalf("%s: the re-lowering pass emitted nothing, so this comparison proves nothing", mod)
			}
			if !bytes.Equal(reused, relowered) {
				t.Errorf("%s: emitted code differs between taking back the rows the inference did not move and "+
					"re-lowering every row (%d bytes reused, %d bytes re-lowered). A row was kept whose lowering "+
					"the inference did move — most likely a caller whose own modes are untouched but whose callee's "+
					"grow mask widened, which drops a bracket rather than refusing. %s",
					mod, len(reused), len(relowered), firstAsmDifference(reused, relowered))
			}
		})
	}
}

func emitSelfHostAsm(t *testing.T, runner []string, fernBin, stdlibRoot, src, tag string, extraEnv []string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), tag+".s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, stdlibRoot)
	cmd.Env = append(os.Environ(), extraEnv...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", tag, err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read asm (%s): %v", tag, err)
	}
	return asm
}

func firstAsmDifference(a, b []byte) string {
	la, lb := bytes.Split(a, []byte("\n")), bytes.Split(b, []byte("\n"))
	for i := 0; i < len(la) && i < len(lb); i++ {
		if !bytes.Equal(la[i], lb[i]) {
			return "First difference at line " + strconv.Itoa(i+1) + ":\n    reused:      " + string(la[i]) +
				"\n    re-lowered:  " + string(lb[i]) + "\n" + nearestLabelBefore(la, i)
		}
	}
	if len(la) != len(lb) {
		return "The two emissions agree line for line up to line " + strconv.Itoa(min(len(la), len(lb))) +
			" and then one ends: " + strconv.Itoa(len(la)) + " lines reused against " + strconv.Itoa(len(lb)) + " re-lowered."
	}
	return ""
}

func nearestLabelBefore(lines [][]byte, at int) string {
	for i := at; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if bytes.HasSuffix(line, []byte(":")) && bytes.HasPrefix(line, []byte("__fn_")) {
			return "    in " + string(bytes.TrimSuffix(line, []byte(":")))
		}
	}
	return "    before the first function label"
}
