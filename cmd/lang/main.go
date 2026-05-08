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
//	lang -fmt FILE.lang                  # write idiomatic, indented source
//	                                     # to stdout (use -w to overwrite
//	                                     # the input file in place; use -d
//	                                     # to print a unified diff against
//	                                     # the on-disk version and exit
//	                                     # non-zero when they differ)
//
// The -cc and -qemu flags override the cross-compiler and emulator.
// Note: the formatter strips `//` line comments and blank lines
// because the lexer drops both before they reach the AST.
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
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

// absPath returns the canonical absolute form of p, or p itself if
// the conversion fails. Used to look up source text in the per-file
// map modload returns.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	target := flag.String("target", "arm32", "code-generation backend: arm32 (default) or wasm")
	cc := flag.String("cc", "arm-linux-gnueabihf-gcc", "ARM cross-compiler used to link when -o or --run is set (arm32 only)")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it under qemu-arm (arm32 only)")
	qemu := flag.String("qemu", "qemu-arm", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	debug := flag.Bool("g", false, "emit DWARF line info + .cfi_* unwind tables (arm32 only); off by default for smaller, faster-startup release binaries")
	wasiPreview2 := flag.Bool("wasi-preview2", false, "wrap the WASM output as a WASI Preview 2 Component Model component (requires wasm-tools + a preview1-component-adapter, see docs/WASI-PREVIEW2.md)")
	wasiAdapter := flag.String("wasi-adapter", "", "path to the wasi_snapshot_preview1.command.wasm adapter (used with -wasi-preview2)")
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-target arm32|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.lang [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       lang -fmt [-w | -d] FILE.lang")
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

	if *doFmt {
		code, err := formatFile(srcPath, *writeBack, *diffMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(code)
	}

	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, *debug, *wasiPreview2, *wasiAdapter, progArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// formatFile parses the file at srcPath, formats it, and either
// writes the result to stdout / back to the file / prints a unified
// diff against the on-disk version. Returns the exit code the CLI
// should use: 0 for "no work needed" or successful write, 1 when
// `-d` saw a difference, with errors returned separately.
func formatFile(srcPath string, writeBack, diffMode bool) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	prog, err := parser.Parse(src)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	formatted := printer.Format(prog)
	if diffMode {
		// `-d` is the conventional CI-friendly mode: print the diff
		// and exit non-zero when the file isn't already formatted.
		// `gofmt -d` follows the same contract.
		diff := printer.UnifiedDiff(src, formatted, srcPath, srcPath)
		if diff == "" {
			return 0, nil
		}
		_, err := os.Stdout.WriteString(diff)
		return 1, err
	}
	if writeBack {
		// Preserve the file's existing mode so chmod state survives
		// the rewrite.
		info, err := os.Stat(srcPath)
		if err != nil {
			return 1, err
		}
		return 0, os.WriteFile(srcPath, []byte(formatted), info.Mode())
	}
	_, err = os.Stdout.WriteString(formatted)
	return 0, err
}

// run drives the full pipeline. The returned int is the exit code that
// the lang process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, debug bool, wasiPreview2 bool, wasiAdapter string, progArgs []string) (int, error) {
	prog, srcs, err := modload.Load(srcPath)
	if err != nil {
		return 1, err
	}
	// `srcs` keys diag back to whichever loaded file the error came
	// from. The checker still reports against the entry file's
	// source for now — multi-file diag plumbing is a future
	// follow-up.
	src := srcs[absPath(srcPath)]
	if err := constfold.Fold(prog); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	// Optimisations now run on the IR (Inline / Fold / DCE inside
	// each backend's Emit), so there's nothing left to do at the
	// AST level after type checking.

	// WASM target: emit WAT to stdout (or -o file) and stop. The arm32
	// link / --run paths don't apply.
	if target == "wasm" {
		text, err := wasm.Emit(prog, info)
		if err != nil {
			return 1, err
		}
		if wasiPreview2 {
			if outPath == "" {
				return 1, fmt.Errorf("-wasi-preview2 requires -o OUTPUT (component is a binary)")
			}
			if wasiAdapter == "" {
				return 1, fmt.Errorf("-wasi-preview2 requires -wasi-adapter PATH (see docs/WASI-PREVIEW2.md)")
			}
			return ifErr(emitPreview2Component(text, outPath, wasiAdapter)), nil
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

	emitOpts := codegen.Options{}
	if debug {
		emitOpts.SourceFile = srcPath
	}
	asm, err := codegen.EmitWithOptions(prog, info, emitOpts)
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

	if err := link(asm, binPath, cc, debug); err != nil {
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
//
// `-nostdlib` drops libc + libgcc + the crt startfiles; we
// provide our own `_start`, syscall wrappers, allocator, and
// memcpy / strcmp / strlen, so the resulting binary contains
// only language code + direct svc 0 syscalls. When `debug` is
// set, `-g` is added so gcc keeps the .file/.loc + .cfi_*
// directives in the form of DWARF line-number tables and
// `.eh_frame`, so gdb / addr2line work on the binary. The
// release default skips both, shrinking hello-world by ~600
// bytes.
func link(asm, outPath, cc string, debug bool) error {
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
	args := []string{"-static", "-nostdlib"}
	if debug {
		args = append(args, "-g")
	} else {
		// `-s` strips the symbol table + .strtab from the
		// final binary. The runtime doesn't read them; they
		// only help interactive debugging, and we already
		// signalled "no debug" by leaving `-g` off.
		args = append(args, "-s")
	}
	args = append(args, asmPath, "-o", outPath)
	cmd := exec.Command(cc, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
}

// emitPreview2Component wraps the preview-1 WAT in a Component Model
// component using wasm-tools + the wasi-preview1-component-adapter.
//
// Pipeline:
//  1. write WAT to a temp file;
//  2. `wasm-tools parse` lowers it to a binary core module;
//  3. `wasm-tools component new --adapt wasi_snapshot_preview1=ADAPTER`
//     wraps the module in a component that satisfies any preview-2
//     host (`wasmtime run`, edge-function runtimes, etc.).
//
// See docs/WASI-PREVIEW2.md for the broader migration plan.
func emitPreview2Component(wat, outPath, adapterPath string) error {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		return fmt.Errorf("wasm-tools not found on PATH (install from https://github.com/bytecodealliance/wasm-tools): %w", err)
	}
	if _, err := os.Stat(adapterPath); err != nil {
		return fmt.Errorf("wasi-preview1-component-adapter not readable at %q: %w", adapterPath, err)
	}
	tmpDir, err := os.MkdirTemp("", "lang-component-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	watPath := filepath.Join(tmpDir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		return err
	}
	modulePath := filepath.Join(tmpDir, "prog.wasm")
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", modulePath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools parse failed: %w\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "component", "new",
		"--adapt", "wasi_snapshot_preview1="+adapterPath,
		modulePath, "-o", outPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component new failed: %w\n%s", err, out)
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
