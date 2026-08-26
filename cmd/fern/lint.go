package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/manifest"
	"github.com/jakechampion/lang/internal/parser"
)

// runLint implements `fern -lint PATH...`: parse each Fern source under
// PATH, run the enabled lint rules over it, and print the findings.
//
// Unlike -check this never type-checks and never resolves an import: a
// lint is about the shape of the code in front of the reader, so a file
// with a type error still lints and a tree of 200 files costs 200 parses.
// That is what makes it usable as a pre-commit gate.
//
// Returns the process exit code: 0 when nothing was denied, 1 when a rule
// at `deny` fired, 2 for a usage or configuration error.
func runLint(paths []string, sets, opts []string, w io.Writer) int {
	files, err := lintTargets(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fern:", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "fern: no .fern files under %s\n", strings.Join(paths, ", "))
		return 2
	}

	cfg, err := lintConfig(files[0], sets, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fern:", err)
		return 2
	}

	// Sources are kept so the renderer can quote the offending line, and
	// so a second finding in the same file does not re-read it.
	srcs := map[string]string{}
	var findings []lint.Finding
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern:", err)
			return 2
		}
		src := string(b)
		prog, err := parser.Parse(src)
		if err != nil {
			// A file that does not parse has no shape to lint. Report it
			// and keep going: linting a tree should not stop at the first
			// broken file the way a compile does.
			fmt.Fprintln(os.Stderr, diag.Format(f, src, err))
			continue
		}
		srcs[f] = src
		found, err := lint.File(cfg, f, src, prog)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern:", err)
			return 2
		}
		findings = append(findings, found...)
	}

	io.WriteString(w, lint.RenderAll(findings, func(f string) string { return srcs[f] }))
	if s := lint.Summary(findings); s != "" {
		fmt.Fprintln(w, s)
	}
	if lint.Failed(findings) {
		return 1
	}
	return 0
}

// lintTargets expands the command line into the list of files to lint: a
// named file is taken as given, a directory is walked for `.fern` sources.
// The result is deduplicated and sorted, so a run over overlapping paths
// reports each file once and two runs print the same order.
func lintTargets(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if strings.HasSuffix(p, ".fern.md") {
				return nil, fmt.Errorf("%s: linting a literate document is not supported yet — tangle it first (`fern -tangle %s`)", p, p)
			}
			if !strings.HasSuffix(p, ".fern") {
				return nil, fmt.Errorf("%s: not a .fern source", p)
			}
			add(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".fern") {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// lintConfig resolves the configuration for a run: the [lint] tables of
// the manifest governing the first target, then the -lint-set / -lint-opt
// flags on top, so a command line always wins over a checked-in setting.
func lintConfig(firstFile string, sets, opts []string) (*lint.Config, error) {
	cfg := lint.NewConfig()

	m, err := manifest.FindForDir(filepath.Dir(firstFile))
	if err != nil {
		return nil, err
	}
	if m != nil && len(m.Lint) > 0 {
		// Sorted so a manifest with several bad entries always reports
		// the same one first.
		keys := make([]string, 0, len(m.Lint))
		for k := range m.Lint {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := cfg.SetPair(k, m.Lint[k]); err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Join(m.Dir, manifest.FileName), err)
			}
		}
	}

	for _, s := range sets {
		name, sev, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("-lint-set %q must be spelled RULE=SEVERITY", s)
		}
		if err := cfg.SetPair(name, sev); err != nil {
			return nil, err
		}
	}
	for _, o := range opts {
		key, val, ok := strings.Cut(o, "=")
		if !ok {
			return nil, fmt.Errorf("-lint-opt %q must be spelled RULE.OPTION=VALUE", o)
		}
		if err := cfg.SetOption(key, val); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// listLintRules implements `fern -lint-rules`: every rule with its default
// severity, its one-line description, and its tunable options.
func listLintRules(w io.Writer) {
	for _, r := range lint.Rules() {
		fmt.Fprintf(w, "%s  [%s]\n", r.Name(), r.DefaultSeverity())
		fmt.Fprintf(w, "    %s\n", r.Doc())
		c, ok := r.(lint.Configurable)
		if !ok {
			continue
		}
		o := c.Options()
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "    option %s.%s = %s (default)\n", r.Name(), k, o[k])
		}
	}
}
