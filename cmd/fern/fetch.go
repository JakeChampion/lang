package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakechampion/lang/internal/manifest"
	"github.com/jakechampion/lang/internal/pkgcache"
)

// runFetch implements `fern -fetch [START]`: locate the manifest
// governing START (a fern.toml, a directory, or any file inside the
// package), then download + verify + unpack every url+hash dependency
// reachable through it — url deps of the root manifest, of its path
// dependencies' manifests, and of already-fetched url dependencies'
// manifests (transitively). Every archive is verified against its
// manifest-declared sha256 before unpacking (pkgcache.Fetch); a
// mismatch fails the whole run. This command is the package manager's
// ONLY network access; the compiler itself never fetches.
func runFetch(start string) error {
	st, err := os.Stat(start)
	dir := start
	switch {
	case err != nil:
		return fmt.Errorf("fetch: %w", err)
	case !st.IsDir():
		dir = filepath.Dir(start)
	}
	root, err := manifest.FindForDir(dir)
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("fetch: no %s governs %s", manifest.FileName, dir)
	}
	seen := map[string]bool{} // manifest dirs walked
	fetched := 0
	var walk func(m *manifest.Manifest) error
	walk = func(m *manifest.Manifest) error {
		if seen[m.Dir] {
			return nil
		}
		seen[m.Dir] = true
		for name, d := range m.Deps {
			var depDir string
			switch {
			case d.URL != "":
				got, present, err := pkgcache.Dir(d.Hash)
				if err != nil {
					return fmt.Errorf("dependency %q: %w", name, err)
				}
				if !present {
					fmt.Fprintf(os.Stderr, "fetching %s (%s)\n", name, d.URL)
					if _, err := pkgcache.Fetch(d.URL, d.Hash); err != nil {
						return fmt.Errorf("dependency %q: %w", name, err)
					}
					fetched++
				}
				depDir = got
			default:
				depDir, _ = m.DepDir(name)
			}
			depMan, err := manifest.Load(depDir)
			if err != nil {
				return fmt.Errorf("dependency %q: %w", name, err)
			}
			if depMan != nil {
				if err := walk(depMan); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "fetch: %d downloaded, %d package(s) checked\n", fetched, len(seen))
	return nil
}
