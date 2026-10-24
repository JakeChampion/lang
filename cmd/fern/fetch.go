package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/mvs"
	"github.com/jakechampion/lang/internal/pkg/pkgcache"
)

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
	seen := map[string]bool{}
	fetched := 0
	var walk func(m *manifest.Manifest) error
	walk = func(m *manifest.Manifest) error {
		if seen[m.Dir] {
			return nil
		}
		seen[m.Dir] = true
		for name, d := range m.Deps {
			if d.Workspace || d.Version != "" {
				continue
			}
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
				return err
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
	locked, err := mvs.ReadLock(root.Dir)
	if err != nil {
		return err
	}
	for name, sel := range locked {
		if sel.Source.URL == "" {
			continue
		}
		if _, present, derr := pkgcache.Dir(sel.Source.Hash); derr == nil && !present {
			fmt.Fprintf(os.Stderr, "fetching %s@%s (%s)\n", name, sel.Version, sel.Source.URL)
			if _, ferr := pkgcache.Fetch(sel.Source.URL, sel.Source.Hash); ferr != nil {
				return fmt.Errorf("locked dependency %q: %w", name, ferr)
			}
			fetched++
		}
	}
	fmt.Fprintf(os.Stderr, "fetch: %d downloaded, %d package(s) checked\n", fetched, len(seen))
	return nil
}
