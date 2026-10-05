package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/jakechampion/lang/internal/launcher"
	"github.com/jakechampion/lang/internal/platforms"
)

// compileRequest is a `-target` compile as the command line spelled it.
// backtrace is nil when -backtrace was not passed, so FERN_BACKTRACE decides.
type compileRequest struct {
	src, out, target, emit, backend, embed, export, qemu string
	runIt, shared, debug, sanitize, cover, optimize      bool
	backtrace                                            *bool
	progArgs                                             []string
}

// compileArgs is the self-host compiler's command line for r, writing to out.
// Without -o the Go CLI printed a native target's assembly, so a native
// compile with no output and no -emit asks for it.
func compileArgs(r compileRequest, out, stdlib string) []string {
	args := []string{"-target", r.target}
	if out != "" {
		args = append(args, "-o", out)
	}
	emit := r.emit
	if d := platforms.ForTarget(r.target); out == "" && emit == "" && (d == nil || d.ISA != "wasm32") {
		emit = "asm"
	}
	for _, f := range []struct{ name, val string }{{"-emit", emit}, {"-backend", r.backend}, {"-embed", r.embed}, {"-export", r.export}} {
		if f.val != "" {
			args = append(args, f.name, f.val)
		}
	}
	for _, f := range []struct {
		name string
		on   bool
	}{{"-g", r.debug}, {"-shared", r.shared}, {"-sanitize", r.sanitize}, {"-cover", r.cover}, {"-O", r.optimize}} {
		if f.on {
			args = append(args, f.name)
		}
	}
	if r.backtrace != nil {
		args = append(args, fmt.Sprintf("-backtrace=%t", *r.backtrace))
	}
	return append(args, r.src, stdlib)
}

// stdoutRefusal refuses a wasm module or component with no -o, as the Go CLI
// did: stdout takes only a target's text.
func stdoutRefusal(r compileRequest) error {
	d := platforms.ForTarget(r.target)
	if r.out != "" || r.runIt || r.emit == "asm" || d == nil || d.ISA != "wasm32" {
		return nil
	}
	if r.emit != "" {
		return fmt.Errorf("-emit %s for -target %s writes a binary; pass -o OUTPUT", r.emit, r.target)
	}
	return fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", r.target)
}

// compileSelfHost hands r to the self-host compiler, and with --run executes
// what it built.
func compileSelfHost(r compileRequest) (int, error) {
	if err := stdoutRefusal(r); err != nil {
		return 1, err
	}
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
		return 1, fmt.Errorf("--run is not supported for -target arm64-darwin (Mach-O binaries need an Apple Silicon Mac to execute; the output at %q is ready to run there)", out)
	}
	// The target's ISA and the host's GOARCH are different vocabularies
	// ("x86-64" against "amd64"), so the comparison goes through the
	// descriptor.
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
