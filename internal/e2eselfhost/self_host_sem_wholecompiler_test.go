package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestSelfHostSemanticWholeCompilerX86_64 is the whole-compiler gate for the
// semantic lowering (docs/SELFHOST-SEMANTIC-SOURCE.md, #9415): the self-host
// compiler built THROUGH the semantic path has to be a compiler, and the same
// one.
//
// Two things are pinned, in the order they are cheapest to lose:
//
//   - Coverage. gen1 is fern.fern compiled by the native-built driver with
//     FERN_SEM_IR=1, and its report must say every declaration produced. A
//     body that stops producing keeps the AST lowering and the module becomes
//     mixed, which is where every crash in this path's history lived; the
//     tally is the one line that says whether that happened.
//   - Output. gen1 compiles lexer.fern, parser.fern, checker.fern and the
//     whole tree byte-identically to the driver, whose lowering of the same
//     inputs is the AST one. This is the primary gate: a fixpoint is blind to
//     a stable miscompile, this is not.
//   - The fixpoint. gen1 compiles the whole tree THROUGH the semantic path
//     byte-identically to the driver doing the same, which is gen1's own
//     text: the compiler the path builds reproduces itself. Secondary, for
//     the reason above, and the gate on the produced code of the semantic
//     modules themselves, which only this run executes.
//   - The AST lowering. A second compiler, built with FERN_SEM_IR= so every
//     declaration takes the AST lowering, compiles literate.fern and
//     lexer.fern through the semantic path byte-identically to the driver.
//     The AST lowering is still the fallback for whatever the semantic path
//     refuses, and a compiler it built once aborted out of bounds on most
//     inputs with nothing gating it (#9763).
//
// Measured 2026-09-16 on a 4-core x86-64 container: gen1 3m at 6.5 GB, the
// four AST emits by both compilers about 2.5 minutes, the semantic emit of
// the whole tree 2m47s by the driver and 4m34s at 4.4 GB by gen1. It runs in
// a job of its own (OWN_JOB_TESTS), not in a shard.
//
// The phases overlap where nothing orders them, under the harness's RAM
// reservation (withBuildMemoryMB) so the overlap fits the host: the gen1
// self-build reserves gen1BuildMB and each whole-tree emit wholeTreeEmitMB,
// so on a 16 GB runner two whole-tree emits run side by side but neither
// runs beside the self-build. The single-module emits are unreserved and
// overlap anything. Which reservation is granted first is the scheduler's
// choice, not this test's; the reservation forbids the overlap either way.
func TestSelfHostSemanticWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := filepath.Abs("../../examples/self_host")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(tree, "fern.fern")
	work := t.TempDir()
	gen1 := filepath.Join(work, "fern-gen1")
	gen1ast := filepath.Join(work, "fern-gen1-ast")

	// One emit each, keyed by compiler and module; "fern" is the whole tree
	// with the AST lowering, "sem" the whole tree through the semantic one, and
	// a module suffixed "@sem" that module through the semantic one.
	type emitKey struct{ compiler, module string }
	emitted := map[emitKey][]byte{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}
	emit := func(compiler, tag, module string) {
		wg.Go(func() {
			src, semantic, reserve := filepath.Join(tree, module+".fern"), false, 0
			if base, ok := strings.CutSuffix(module, "@sem"); ok {
				src, semantic = filepath.Join(tree, base+".fern"), true
			}
			switch module {
			case "sem":
				src, semantic, reserve = entry, true, wholeTreeEmitMB
			case "fern":
				reserve = wholeTreeEmitMB
			}
			var asm []byte
			err := withBuildMemoryMB(reserve, func() (err error) {
				asm, err = emitAsm(compiler, src, stdlibRoot, filepath.Join(work, module+"-"+tag+".s"), semantic)
				return err
			})
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			emitted[emitKey{tag, module}] = asm
			mu.Unlock()
		})
	}
	modules := []string{"lexer", "parser", "checker", "fern"}
	astModules := []string{"literate@sem", "lexer@sem"}

	var report string
	wg.Go(func() {
		err := withBuildMemoryMB(gen1BuildMB, func() (err error) {
			report, err = semSelfBuild(driver, entry, stdlibRoot, gen1)
			return err
		})
		if err != nil {
			fail(err)
		}
	})
	wg.Go(func() {
		err := withBuildMemoryMB(astGen1BuildMB, func() error {
			return astSelfBuild(driver, entry, stdlibRoot, gen1ast)
		})
		if err != nil {
			fail(err)
		}
	})
	emit(driver, "driver", "sem")
	for _, m := range append(modules, astModules...) {
		emit(driver, "driver", m)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	produced, total := semTally(t, report)
	if produced != total {
		t.Fatalf("gen1 produced %d of %d declarations; every refusal is a body the AST lowering emits into a mixed module:\n%s",
			produced, total, report)
	}

	emit(gen1, "gen1", "sem")
	for _, m := range modules {
		emit(gen1, "gen1", m)
	}
	for _, m := range astModules {
		emit(gen1ast, "gen1-ast", m)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}

	for _, m := range modules {
		if got, want := emitted[emitKey{"gen1", m}], emitted[emitKey{"driver", m}]; !bytes.Equal(got, want) {
			t.Fatalf("gen1 compiles %s.fern differently from the AST-lowered driver (%d bytes against %d)", m, len(got), len(want))
		}
	}
	if got, want := emitted[emitKey{"gen1", "sem"}], emitted[emitKey{"driver", "sem"}]; !bytes.Equal(got, want) {
		t.Fatalf("gen1 compiles the whole tree through the semantic path differently from the driver (%d bytes against %d): the fixpoint does not hold", len(got), len(want))
	}
	for _, m := range astModules {
		if got, want := emitted[emitKey{"gen1-ast", m}], emitted[emitKey{"driver", m}]; !bytes.Equal(got, want) {
			t.Fatalf("the compiler built through the AST lowering compiles %s differently from the driver (%d bytes against %d)", m, len(got), len(want))
		}
	}
}

// RAM reservations for the steps above, from their measured peak RSS on
// 2026-09-22 (4-core x86-64 container): the gen1 self-build was killed at
// 8.2 GB by a 14 GB cgroup while a whole-tree emit ran beside it, the
// whole-tree AST emit peaks 5.3 GB and the semantic one 4.2 GB; a module emit
// stays under 0.5 GB. The margins are what keep the self-build from sharing
// a 16 GB host's budget with a whole-tree emit while two emits still fit.
const (
	gen1BuildMB     = 9500
	wholeTreeEmitMB = 6000
	astGen1BuildMB  = 6500
)

// astSelfBuild compiles entry to a linked x86-64 binary at out with every
// declaration through the AST lowering (5.4 GB peak, 2026-09-23).
func astSelfBuild(compiler, entry, stdlibRoot, out string) error {
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", out, entry, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s building %s through the AST lowering: %v\n%s", filepath.Base(compiler), filepath.Base(entry), err, msg)
	}
	return os.Chmod(out, 0o755)
}

// semSelfBuild compiles entry to a linked x86-64 binary at out through the
// semantic lowering, and returns the compiler's report.
func semSelfBuild(compiler, entry, stdlibRoot, out string) (string, error) {
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", out, entry, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s building %s through the semantic lowering: %v\n%s", filepath.Base(compiler), filepath.Base(entry), err, stderr.String())
	}
	if err := os.Chmod(out, 0o755); err != nil {
		return "", err
	}
	return stderr.String(), nil
}

var semTallyLine = regexp.MustCompile(`module: produced (\d+) of (\d+) declarations`)

// semTally reads the produced and total declaration counts out of the report.
func semTally(t *testing.T, report string) (int, int) {
	t.Helper()
	m := semTallyLine.FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no production tally in the report:\n%s", report)
	}
	var produced, total int
	for i, dst := range []*int{&produced, &total} {
		for _, c := range m[i+1] {
			*dst = *dst*10 + int(c-'0')
		}
	}
	return produced, total
}

// emitAsm compiles src to x86-64 assembly text with the semantic lowering
// off (the two compilers compared on the lowering they both carry) or on
// (the fixpoint).
func emitAsm(compiler, src, stdlibRoot, out string, semantic bool) ([]byte, error) {
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-emit", "asm", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=")
	if semantic {
		cmd.Env = append(cmd.Env, "FERN_SEM_IR=1")
	}
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s on %s: %v\n%s", filepath.Base(compiler), filepath.Base(src), err, msg)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if len(asm) == 0 {
		return nil, fmt.Errorf("%s emitted nothing for %s", filepath.Base(compiler), filepath.Base(src))
	}
	return asm, nil
}
