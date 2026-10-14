package main

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/jakechampion/lang/internal/tools/launcher"
)

// runLint implements `fern -lint PATH...` with the self-host compiler's linter
// (compiler/lint.fern): the findings go to w, a usage or configuration error
// to stderr, and the exit status is the linter's (1 when a rule set to deny
// fires, 2 for a usage error).
func runLint(paths, sets, opts []string, color string, w io.Writer) int {
	args := []string{"-lint", "-color", color}
	for _, s := range sets {
		args = append(args, "-lint-set", s)
	}
	for _, o := range opts {
		args = append(args, "-lint-opt", o)
	}
	return runSelfHostTool(append(args, paths...), w)
}

// listLintRules implements `fern -lint-rules`.
func listLintRules(w io.Writer) int {
	return runSelfHostTool([]string{"-lint-rules"}, w)
}

// runSelfHostTool runs the self-host compiler with args, its stdout to w and
// its stderr to this process's, and returns its exit status.
func runSelfHostTool(args []string, w io.Writer) int {
	compiler, err := launcher.Compiler()
	if err != nil {
		io.WriteString(os.Stderr, err.Error()+"\n")
		return 2
	}
	cmd := exec.Command(compiler, args...)
	cmd.Stdout, cmd.Stderr = w, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		io.WriteString(os.Stderr, "fern: "+err.Error()+"\n")
		return 2
	}
	return 0
}
