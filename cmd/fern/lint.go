package main

import "io"

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

func listLintRules(w io.Writer) int {
	return runSelfHostTool([]string{"-lint-rules"}, w)
}
