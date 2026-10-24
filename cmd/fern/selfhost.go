package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/tools/launcher"
)

type compileRequest struct {
	src, out, target, emit, backend, embed, export, qemu    string
	runIt, shared, debug, sanitize, cover, optimize, harden bool
	backtrace, size                                         *bool
	progArgs                                                []string
}

func compileArgs(r compileRequest, out, stdlib string) []string {
	args := []string{"-target", r.target}
	if out != "" {
		args = append(args, "-o", out)
	}
	for _, f := range []struct{ name, val string }{{"-emit", r.emit}, {"-backend", r.backend}, {"-embed", r.embed}, {"-export", r.export}} {
		if f.val != "" {
			args = append(args, f.name, f.val)
		}
	}
	for _, f := range []struct {
		name string
		on   bool
	}{{"-g", r.debug}, {"-shared", r.shared}, {"-sanitize", r.sanitize}, {"-cover", r.cover}, {"-O", r.optimize}, {"-harden", r.harden}} {
		if f.on {
			args = append(args, f.name)
		}
	}
	if r.size != nil {
		args = append(args, fmt.Sprintf("-size=%t", *r.size))
	}
	if r.backtrace != nil {
		args = append(args, fmt.Sprintf("-backtrace=%t", *r.backtrace))
	}
	return append(args, r.src, stdlib)
}

func wasmRunRefusal(r compileRequest) error {
	if r.target == "wasm32-wasi-http" {
		return fmt.Errorf("-run cannot serve -target wasm32-wasi-http; build it with -o OUT.wasm and run `wasmtime serve OUT.wasm`")
	}
	return nil
}

func compileSelfHost(r compileRequest) (int, error) {
	compiler, err := launcher.Compiler()
	if err != nil {
		return 1, err
	}
	stdlib, err := launcher.StdlibRoot()
	if err != nil {
		return 1, err
	}
	out := r.out
	if r.runIt && out == "" {
		dir, err := os.MkdirTemp("", "fern-run-")
		if err != nil {
			return 1, err
		}
		defer os.RemoveAll(dir)
		out = filepath.Join(dir, "prog")
	}
	cmd := exec.Command(compiler, compileArgs(r, out, stdlib)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, err
	}
	if !r.runIt {
		return 0, nil
	}
	if r.target == "arm64-darwin" {
		return 1, fmt.Errorf("-run is not supported for -target arm64-darwin (Mach-O binaries need an Apple Silicon Mac to execute; the output at %q is ready to run there)", out)
	}
	if d := platforms.ForTarget(r.target); d != nil && d.ISA == "wasm32" {
		if err := wasmRunRefusal(r); err != nil {
			return 1, err
		}
		return execTool("wasmtime", append([]string{"run", out}, r.progArgs...))
	}
	if d := platforms.ForTarget(r.target); d != nil {
		if (d.ISA == "arm64" && runtime.GOARCH == "arm64") || (d.ISA == "x86-64" && runtime.GOARCH == "amd64") {
			return execDirect(out, r.progArgs)
		}
	}
	qemu := r.qemu
	if r.target == "x86-64-linux" && qemu == "qemu-aarch64" {
		qemu = "qemu-x86_64"
	}
	return execUnderQemu(qemu, out, r.progArgs)
}

func runREPL() int {
	stdlib, err := launcher.StdlibRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return runSelfHostTool([]string{"-repl", stdlib}, os.Stdout)
}

func runEffects(src string) int {
	stdlib, err := launcher.StdlibRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return runSelfHostTool([]string{"-effects", src, stdlib}, os.Stdout)
}

func runLiterate(doc, mode, out, chunk string, html bool) int {
	args := []string{mode}
	if out != "" {
		args = append(args, "-o", out)
	}
	if chunk != "" {
		args = append(args, "-chunk", chunk)
	}
	if html {
		args = append(args, "-html")
	}
	args = append(args, doc)
	if mode == "-doctest" {
		stdlib, err := launcher.StdlibRoot()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		args = append(args, stdlib)
	}
	return runSelfHostTool(args, os.Stdout)
}

func runSelfHostTool(args []string, w io.Writer) int {
	compiler, err := launcher.Compiler()
	if err != nil {
		io.WriteString(os.Stderr, err.Error()+"\n")
		return 2
	}
	cmd := exec.Command(compiler, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, w, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		io.WriteString(os.Stderr, "fern-oracle: "+err.Error()+"\n")
		return 2
	}
	return 0
}
