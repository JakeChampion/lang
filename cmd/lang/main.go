// Command lang compiles a single .lang source file.
//
// Usage:
//
//	lang FILE.lang                       # write ARM32 assembly to stdout
//	lang -o OUTPUT FILE.lang             # link with the ARM cross-compiler
//	                                     # and write a static ELF binary
//	lang --run FILE.lang [-- ARGS...]    # link to a temporary binary and
//	                                     # execute it under qemu-arm,
//	                                     # forwarding stdio
//
// The -cc and -qemu flags override the cross-compiler and emulator.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	cc := flag.String("cc", "arm-linux-gnueabihf-gcc", "ARM cross-compiler used to link when -o or --run is set")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it under qemu-arm")
	qemu := flag.String("qemu", "qemu-arm", "user-mode emulator used by --run")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.lang [-- ARGS...]")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath := flag.Arg(0)
	progArgs := flag.Args()[1:] // anything after the source path is forwarded to the program

	code, err := run(srcPath, *out, *cc, *runIt, *qemu, progArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run drives the full pipeline. The returned int is the exit code that
// the lang process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, cc string, runIt bool, qemu string, progArgs []string) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)

	prog, err := parser.Parse(src)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(src, err))
	}
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		return 1, err
	}

	if !runIt && outPath == "" {
		if _, err := os.Stdout.WriteString(asm); err != nil {
			return 1, err
		}
		return 0, nil
	}

	// Decide where the binary lives. With --run and no -o we link to a
	// temp file we'll throw away after execution.
	binPath := outPath
	var cleanupBin string
	if binPath == "" {
		f, err := os.CreateTemp("", "lang-bin-*")
		if err != nil {
			return 1, err
		}
		f.Close()
		binPath = f.Name()
		cleanupBin = binPath
	}
	defer func() {
		if cleanupBin != "" {
			os.Remove(cleanupBin)
		}
	}()

	if err := link(asm, binPath, cc); err != nil {
		return 1, err
	}
	if !runIt {
		return 0, nil
	}
	return execUnderQemu(qemu, binPath, progArgs)
}

// link writes asm to a temp .s file and invokes the cross-compiler to
// produce a static binary at outPath. The temp file is removed on
// success; on failure we leave it in place so the user can inspect it.
func link(asm, outPath, cc string) error {
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("cross-compiler %q not found on PATH (override with -cc): %w", cc, err)
	}
	tmpDir, err := os.MkdirTemp("", "lang-build-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(tmpDir)
		}
	}()

	asmPath := filepath.Join(tmpDir, "prog.s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return err
	}
	cmd := exec.Command(cc, "-static", asmPath, "-o", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
}

// execUnderQemu runs binPath through the supplied user-mode emulator
// with stdio passed through. The first return is the program's exit
// code (so the caller can mirror it as the lang process exit code).
func execUnderQemu(qemu, binPath string, progArgs []string) (int, error) {
	if _, err := exec.LookPath(qemu); err != nil {
		return 1, fmt.Errorf("emulator %q not found on PATH (override with -qemu): %w", qemu, err)
	}
	args := append([]string{binPath}, progArgs...)
	cmd := exec.Command(qemu, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return 1, err
}
