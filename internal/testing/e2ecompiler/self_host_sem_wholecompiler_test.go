package e2ecompiler

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestSelfHostSemanticWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := filepath.Abs("../../../compiler")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(tree, "fern.fern")
	work := t.TempDir()
	gen1 := filepath.Join(work, "fern-gen1")

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
			src, reserve := filepath.Join(tree, module+".fern"), 0
			if module == "tree" {
				src, reserve = entry, wholeTreeEmitMB
			}
			var asm []byte
			err := withBuildMemoryMB(reserve, func() (err error) {
				asm, err = emitAsm(compiler, src, stdlibRoot, filepath.Join(work, module+"-"+tag+".s"))
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
	modules := []string{"lexer", "parser", "checker"}

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
	emit(driver, "driver", "tree")
	for _, m := range modules {
		emit(driver, "driver", m)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	produced, total := semTally(t, report)
	if produced != total {
		t.Fatalf("gen1 produced %d of %d declarations and instances:\n%s",
			produced, total, report)
	}

	emit(gen1, "gen1", "tree")
	for _, m := range modules {
		emit(gen1, "gen1", m)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}

	divergence := func(gotName string, got, want []byte) string {
		bin, err := os.ReadFile(driver)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("(%d bytes against %d; driver %s, sha256 %x)\n%s", len(got), len(want), driver, sha256.Sum256(bin),
			firstDiffLines("driver", string(want), gotName, string(got)))
	}
	for _, m := range modules {
		if got, want := emitted[emitKey{"gen1", m}], emitted[emitKey{"driver", m}]; !bytes.Equal(got, want) {
			t.Fatalf("gen1 compiles %s.fern differently from the driver %s", m, divergence("gen1", got, want))
		}
	}
	if got, want := emitted[emitKey{"gen1", "tree"}], emitted[emitKey{"driver", "tree"}]; !bytes.Equal(got, want) {
		t.Fatalf("gen1 compiles the whole tree differently from the driver, so the fixpoint does not hold %s", divergence("gen1", got, want))
	}
}

const (
	gen1BuildMB     = 9500
	wholeTreeEmitMB = 6000
)

func semSelfBuild(compiler, entry, stdlibRoot, out string) (string, error) {
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", out, entry, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR_REPORT=1")
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

var semTallyLine = regexp.MustCompile(`module: produced (\d+) of (\d+) declarations and (\d+) of (\d+) instances`)

func semTally(t *testing.T, report string) (int, int) {
	t.Helper()
	lines := semTallyLine.FindAllStringSubmatch(report, -1)
	if lines == nil {
		t.Fatalf("no production tally in the report:\n%s", report)
	}
	var produced, total int
	for _, m := range lines {
		for i, c := range m[1:] {
			n, err := strconv.Atoi(c)
			if err != nil {
				t.Fatalf("unreadable tally %q: %v", m[0], err)
			}
			if i%2 == 0 {
				produced += n
			} else {
				total += n
			}
		}
	}
	return produced, total
}

func emitAsm(compiler, src, stdlibRoot, out string) ([]byte, error) {
	cmd := exec.Command(compiler, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, stdlibRoot)
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
