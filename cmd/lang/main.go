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
	"github.com/jakechampion/lang/internal/codegen/wasm"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/optimizer"
	"github.com/jakechampion/lang/internal/parser"
)

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	target := flag.String("target", "arm32", "code-generation backend: arm32 (default) or wasm")
	cc := flag.String("cc", "arm-linux-gnueabihf-gcc", "ARM cross-compiler used to link when -o or --run is set (arm32 only)")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it under qemu-arm (arm32 only)")
	qemu := flag.String("qemu", "qemu-arm", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-target arm32|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.lang [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       lang -repl")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *repl {
		if err := interp.REPL(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath := flag.Arg(0)
	progArgs := flag.Args()[1:] // anything after the source path is forwarded to the program

	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, progArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run drives the full pipeline. The returned int is the exit code that
// the lang process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, progArgs []string) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)

	prog, err := parser.Parse(src)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	optimizer.Optimize(prog)

	// WASM target: emit WAT to stdout (or -o file) and stop. The arm32
	// link / --run paths don't apply.
	if target == "wasm" {
		text, err := wasm.Emit(prog, info)
		if err != nil {
			return 1, err
		}
		if outPath == "" {
			_, err = os.Stdout.WriteString(text)
			return ifErr(err), err
		}
		return ifErr(os.WriteFile(outPath, []byte(text), 0o644)), nil
	}
	if target != "arm32" {
		return 1, fmt.Errorf("unknown target %q (want arm32 or wasm)", target)
	}

	asm, err := codegen.EmitWithOptions(prog, info, codegen.Options{SourceFile: srcPath})
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
	// `-g` keeps the .file/.loc directives we emit in the form of
	// DWARF line-number tables, so gdb / addr2line work on the binary.
	cmd := exec.Command(cc, "-static", "-g", asmPath, "-o", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
}

// ifErr collapses a Go error into the exit code lang should return.
func ifErr(err error) int {
	if err != nil {
		return 1
	}
	return 0
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
