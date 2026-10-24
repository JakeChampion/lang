package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/mvs"
	"github.com/jakechampion/lang/internal/pkg/pkgcache"
)

func runResolve(start string) error {
	st, err := os.Stat(start)
	dir := start
	switch {
	case err != nil:
		return fmt.Errorf("resolve: %w", err)
	case !st.IsDir():
		dir = filepath.Dir(start)
	}
	root, err := manifest.FindForDir(dir)
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("resolve: no %s governs %s", manifest.FileName, dir)
	}
	rootDeps := root.VersionDeps()
	if len(rootDeps) == 0 {
		return fmt.Errorf("resolve: %s declares no versioned dependencies (nothing to resolve)", filepath.Join(root.Dir, manifest.FileName))
	}
	if root.Index == "" {
		return fmt.Errorf("resolve: %s has versioned dependencies but no `index = \"…\"` under [package]", filepath.Join(root.Dir, manifest.FileName))
	}
	indexPath := root.Index
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root.Dir, indexPath)
	}
	ix, err := mvs.LoadIndex(indexPath)
	if err != nil {
		return err
	}

	depsOf := func(pkg string, v mvs.Version, src mvs.Source) (map[string]string, error) {
		d, err := materialize(pkg, v, src)
		if err != nil {
			return nil, err
		}
		m, err := manifest.Load(d)
		if err != nil {
			return nil, err
		}
		if m == nil {
			return nil, fmt.Errorf("%s@%s has no %s", pkg, v, manifest.FileName)
		}
		return m.VersionDeps(), nil
	}

	sel, err := mvs.Resolve(rootDeps, root.Excludes, ix, depsOf)
	if err != nil {
		return err
	}
	if err := mvs.WriteLock(root.Dir, sel); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "resolve: %d package(s) pinned in %s\n", len(sel), filepath.Join(root.Dir, mvs.LockFileName))
	for _, s := range sel {
		fmt.Fprintf(os.Stderr, "  %s %s\n", s.Name, s.Version)
	}
	return nil
}

func materialize(pkg string, v mvs.Version, src mvs.Source) (string, error) {
	if src.Path != "" {
		return src.Path, nil
	}
	dir, err := pkgcache.Fetch(src.URL, src.Hash)
	if err != nil {
		return "", fmt.Errorf("%s@%s: %w", pkg, v, err)
	}
	return dir, nil
}
