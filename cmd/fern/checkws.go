package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakechampion/lang/internal/manifest"
)

// runCheckTarget dispatches `fern -check ARG`:
//   - `-` or a file → the single-entry type-check (runCheck), unchanged.
//   - a directory that is a [workspace] root → type-check EVERY member
//     package, aggregating results and failing if any member fails.
//   - a directory that is a plain package → type-check that package's
//     entry module.
//
// Checking a package means type-checking its entry module — the `lib`
// module its manifest names (default lib.fern), or `main.fern` for an
// application member that has no lib. This is the workspace-wide check
// the multi-package self-hosted compiler wants: one command validates
// lexer / parser / checker / codegen together.
func runCheckTarget(arg string) error {
	if arg == "-" {
		return runCheck(arg)
	}
	st, err := os.Stat(arg)
	if err != nil || !st.IsDir() {
		return runCheck(arg) // a file (or a bad path runCheck will report)
	}
	man, err := manifest.FindForDir(arg)
	if err != nil {
		return err
	}
	if man == nil {
		return fmt.Errorf("check: %s is a directory with no fern.toml — pass a .fern file, or run inside a package", arg)
	}
	if !man.IsWorkspace() {
		entry, err := packageEntry(man.Dir)
		if err != nil {
			return err
		}
		return runCheck(entry)
	}

	// Workspace: check each member, aggregating. Don't stop at the first
	// failure — report every member's status so one broken package
	// doesn't hide the rest.
	failed := 0
	for _, rel := range man.Members {
		dir := man.MemberDir(rel)
		entry, err := packageEntry(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", rel, err)
			failed++
			continue
		}
		if cerr := runCheck(entry); cerr != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s\n%v\n", rel, cerr)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "ok   %s\n", rel)
	}
	if failed > 0 {
		return fmt.Errorf("check: %d of %d workspace member(s) failed", failed, len(man.Members))
	}
	fmt.Fprintf(os.Stderr, "check: %d workspace member(s) ok\n", len(man.Members))
	return nil
}

// packageEntry returns the entry module to type-check for the package
// rooted at dir: its manifest's `lib` module when that file exists, else
// `main.fern` (an application member), else an error naming both.
func packageEntry(dir string) (string, error) {
	man, err := manifest.Load(dir)
	if err != nil {
		return "", err
	}
	lib := manifest.DefaultLib
	if man != nil {
		lib = man.Lib
	}
	if p := filepath.Join(dir, lib); fileExists(p) {
		return p, nil
	}
	if p := filepath.Join(dir, "main.fern"); fileExists(p) {
		return p, nil
	}
	return "", fmt.Errorf("no entry module in %s (looked for %s and main.fern)", dir, lib)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
