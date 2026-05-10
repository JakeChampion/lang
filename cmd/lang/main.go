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
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/wasm"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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
	target := flag.String("target", "arm32", "code-generation backend: arm32 (default), wasm (CLI component), or wasi-http (HTTP handler component implementing wasi:http/incoming-handler)")
	cc := flag.String("cc", "arm-linux-gnueabihf-gcc", "ARM cross-compiler used to link when -o or --run is set (arm32 only)")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it under qemu-arm (arm32 only)")
	qemu := flag.String("qemu", "qemu-arm", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	debug := flag.Bool("g", false, "emit DWARF line info + .cfi_* unwind tables (arm32 only); off by default for smaller, faster-startup release binaries")
	wasiAdapter := flag.String("wasi-adapter", "", "path to the wasi_snapshot_preview1.command.wasm adapter (required for -target wasm; see docs/WASI-PREVIEW2.md)")
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

	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, *debug, *wasiAdapter, progArgs)
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
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, debug bool, wasiAdapter string, progArgs []string) (int, error) {
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
	// Monomorphise generic functions before any later stage sees
	// the program — IR / codegen / interp only ever deal with
	// concrete, name-mangled clones. No-op when the program has
	// no generic decls.
	if err := monomorph.Run(prog, info); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	// Tree-shake (removing unreferenced prelude helpers) is
	// done inside each backend's Emit, so it sees a fully
	// monomorphised program here without main.go having to
	// orchestrate it.
	// Optimisations now run on the IR (Inline / Fold / DCE inside
	// each backend's Emit), so there's nothing left to do at the
	// AST level after type checking.

	// WASM target: always emit a WASI Preview 2 Component Model
	// component. Preview-1 was the historical fallback while the
	// migration in docs/WASI-PREVIEW2.md was in flight; once every
	// builtin had a preview-2 path (steps 2-5), the preview-1 emit
	// was retired in step 6.
	if target == "wasm" || target == "wasi-http" {
		opts := wasm.EmitOptions{}
		world := "lang"
		if target == "wasi-http" {
			// Step 5: HTTP handler target. The world EXPORTS
			// wasi:http/incoming-handler — `wasmtime serve`
			// dispatches inbound requests into the user's
			// `handle(req: HttpRequest): HttpResponse`. No
			// `_start`; no `main()` is required.
			opts.HttpHandler = true
			world = "http"
		}
		text, err := wasm.EmitWithOptions(prog, info, opts)
		if err != nil {
			return 1, err
		}
		if outPath == "" {
			return 1, fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", target)
		}
		if wasiAdapter == "" {
			return 1, fmt.Errorf("-target %s requires -wasi-adapter PATH (see docs/WASI-PREVIEW2.md)", target)
		}
		if err := emitPreview2ComponentWorld(text, outPath, wasiAdapter, world); err != nil {
			return 1, err
		}
		return 0, nil
	}
	// arm64 (aarch64): Linux ELF binaries for arm64 hosts.
	// Apple Silicon Macs run these via Docker / OrbStack /
	// UTM containers; servers (Raspberry Pi 4+ in 64-bit mode,
	// AWS Graviton, Android) run them natively. Native arm64
	// macOS (Mach-O binaries) is a separate target waiting on
	// the syscall ABI + Mach-O emit work.
	if target == "arm64" {
		asm, err := arm64codegen.Emit(prog, info)
		if err != nil {
			return 1, err
		}
		// Replace arm32-default cc / qemu with arm64 defaults
		// when the user didn't override them. The flag parser
		// can't see target before parsing finishes, so we do
		// the late-bound default-replacement here.
		if cc == "arm-linux-gnueabihf-gcc" {
			cc = "aarch64-linux-gnu-gcc"
		}
		if qemu == "qemu-arm" {
			qemu = "qemu-aarch64"
		}
		if !runIt && outPath == "" {
			if _, err := os.Stdout.WriteString(asm); err != nil {
				return 1, err
			}
			return 0, nil
		}
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
	if target != "arm32" {
		return 1, fmt.Errorf("unknown target %q (want arm32, arm64, wasm, or wasi-http)", target)
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

// witFS bundles the WIT package(s) the wasm backend's preview-2
// imports refer to. Embedding lets `lang` ship as a single binary —
// `wasm-tools component embed` reads them from a temp directory we
// extract at preview-2 emission time. Add new WIT under
// cmd/lang/wit/ and they'll be picked up automatically.
//
//go:embed wit
var witFS embed.FS

// emitPreview2ComponentWorld wraps the WAT in a Component Model
// component matching the named WIT world (currently `lang` for the
// CLI target or `http` for the HTTP-handler target). Pipeline:
//  1. write WAT to a temp file;
//  2. `wasm-tools parse` lowers it to a binary core module;
//  3. `wasm-tools component embed` annotates the module with the
//     `local:lang/<world>` WIT world so the component-new step
//     knows how to lift the native preview-2 imports we emit and,
//     for the http world, where the exported
//     `wasi:http/incoming-handler.handle` lives;
//  4. `wasm-tools component new --adapt wasi_snapshot_preview1=ADAPTER`
//     composes the module with the adapter; preview-1 imports
//     (args/env/proc_exit) get translated to preview-2 by the
//     adapter, native preview-2 imports flow through unchanged.
//     The result satisfies any preview-2 host (`wasmtime run`,
//     `wasmtime serve`, edge-function runtimes, etc.).
func emitPreview2ComponentWorld(wat, outPath, adapterPath, world string) error {
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

	witDir := filepath.Join(tmpDir, "wit")
	if err := extractWIT(witDir); err != nil {
		return fmt.Errorf("extract embedded WIT: %w", err)
	}

	watPath := filepath.Join(tmpDir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		return err
	}
	modulePath := filepath.Join(tmpDir, "prog.wasm")
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", modulePath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools parse failed: %w\n%s", err, out)
	}
	embeddedPath := filepath.Join(tmpDir, "prog.embedded.wasm")
	if out, err := exec.Command("wasm-tools", "component", "embed",
		witDir, "-w", world,
		modulePath, "-o", embeddedPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component embed failed: %w\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "component", "new",
		"--adapt", "wasi_snapshot_preview1="+adapterPath,
		embeddedPath, "-o", outPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component new failed: %w\n%s", err, out)
	}
	return nil
}

// extractWIT walks the embedded `wit/` tree and writes it under
// dstRoot, preserving the relative directory structure. Used to
// hand a real on-disk path to `wasm-tools component embed`, which
// resolves WIT imports through the filesystem.
func extractWIT(dstRoot string) error {
	return fs.WalkDir(witFS, "wit", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("wit", p)
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := witFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
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
