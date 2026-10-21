package main

import "io"

// runLint implements `fern -lint PATH...` with the self-host compiler's linter
// (compiler/lint.fern): the findings go to w, a usage or configuration error
// to stderr, and the exit status is the linter's (1 when a rule set to deny
// fires, 2 for a usage error).
func runLint(paths, sets, opts []string, cover, color string, w io.Writer) int {
	args := []string{"-lint", "-color", color}
	for _, s := range sets {
		args = append(args, "-lint-set", s)
	}
	for _, o := range opts {
		args = append(args, "-lint-opt", o)
	}
	if cover != "" {
		args = append(args, "-lint-cover", cover)
	}
	return runSelfHostTool(append(args, paths...), w)
}

// listLintRules implements `fern -lint-rules`.
func listLintRules(w io.Writer) int {
	return runSelfHostTool([]string{"-lint-rules"}, w)
}
