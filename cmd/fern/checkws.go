package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/platforms"
)

func runCheckTarget(arg, target string) error {
	if target != "" && platforms.ForTarget(target) == nil {
		return errUnknownTarget(target)
	}
	if arg == "-" {
		return runCheck(arg, target)
	}
	st, err := os.Stat(arg)
	if err != nil || !st.IsDir() {
		return runCheck(arg, target)
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
		return runCheck(entry, target)
	}

	failed := 0
	for _, rel := range man.Members {
		dir := man.MemberDir(rel)
		entry, err := packageEntry(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", rel, err)
			failed++
			continue
		}
		if cerr := runCheck(entry, target); cerr != nil {
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

func errUnknownTarget(target string) error {
	return fmt.Errorf("unknown target %q — run `fern-oracle -targets` for the list (targets are <isa>-<environment>, e.g. arm64-linux, x86-64-linux, wasm32-wasi)", target)
}
