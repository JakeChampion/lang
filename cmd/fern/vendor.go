package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/manifest"
	"github.com/jakechampion/lang/internal/pkgcache"
)

// runVendor implements `fern -vendor [START]`: flatten the full
// transitive dependency graph of the manifest governing START into
// `<root>/vendor/<name>/`, one directory per package keyed by its
// manifest `name`. After vendoring, builds are fully offline — the
// loader resolves every declared dependency out of `vendor/` and never
// touches the network or the deps' original path/url locations (Rec §6,
// the `BOOTSTRAP-RESEARCH.md` no-build-time-network constraint).
//
// url dependencies must already be in the content-addressed store
// (`fern -fetch` first); vendoring copies from the store, it does not
// download. A name collision between two distinct packages is a hard
// error — the flat namespace requires unique names.
func runVendor(start string) error {
	st, err := os.Stat(start)
	dir := start
	switch {
	case err != nil:
		return fmt.Errorf("vendor: %w", err)
	case !st.IsDir():
		dir = filepath.Dir(start)
	}
	root, err := manifest.FindForDir(dir)
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("vendor: no %s governs %s", manifest.FileName, dir)
	}

	// Collect every transitive package's source directory, keyed by name.
	// Walk manifests (not the vendor tree — we're building it), following
	// path deps to their directories and url deps to the store.
	srcOf := map[string]string{} // name -> source directory
	seenDir := map[string]bool{}
	var walk func(m *manifest.Manifest) error
	walk = func(m *manifest.Manifest) error {
		if seenDir[m.Dir] {
			return nil
		}
		seenDir[m.Dir] = true
		for name, d := range m.Deps {
			// Workspace-member deps live in the workspace tree (resolved by
			// name), not in vendor/ — skip them.
			if d.Workspace {
				continue
			}
			depDir, err := depSourceDir(m, name, d)
			if err != nil {
				return err
			}
			depMan, err := manifest.Load(depDir)
			if err != nil {
				return fmt.Errorf("dependency %q: %w", name, err)
			}
			key := name
			if depMan != nil && depMan.Name != "" {
				key = depMan.Name
			}
			if prev, ok := srcOf[key]; ok && prev != depDir {
				return fmt.Errorf("vendor: two different packages both named %q (%s and %s) — package names must be unique to vendor", key, prev, depDir)
			}
			srcOf[key] = depDir
			if depMan != nil {
				if err := walk(depMan); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// A workspace root vendors the UNION of all members' external
	// dependencies into the root's vendor/ (members resolve out of it, per
	// the resolver's workspace-root vendor fallback). A plain package
	// vendors its own transitive deps.
	if root.IsWorkspace() {
		for _, rel := range root.Members {
			mm, err := manifest.Load(root.MemberDir(rel))
			if err != nil {
				return fmt.Errorf("vendor: member %q: %w", rel, err)
			}
			if mm == nil {
				return fmt.Errorf("vendor: workspace member %q has no %s", rel, manifest.FileName)
			}
			if err := walk(mm); err != nil {
				return err
			}
		}
	} else if err := walk(root); err != nil {
		return err
	}

	vendorDir := filepath.Join(root.Dir, "vendor")
	if err := os.RemoveAll(vendorDir); err != nil {
		return fmt.Errorf("vendor: clearing %s: %w", vendorDir, err)
	}
	for name, src := range srcOf {
		dst := filepath.Join(vendorDir, name)
		if err := copyPackage(src, dst); err != nil {
			return fmt.Errorf("vendor: copying %q: %w", name, err)
		}
	}
	fmt.Fprintf(os.Stderr, "vendor: %d package(s) written to %s\n", len(srcOf), vendorDir)
	return nil
}

// depSourceDir resolves a dependency to its on-disk source directory: a
// path dep's directory, or an already-fetched url dep's store directory.
func depSourceDir(m *manifest.Manifest, name string, d manifest.Dep) (string, error) {
	if d.URL != "" {
		store, present, err := pkgcache.Dir(d.Hash)
		if err != nil {
			return "", fmt.Errorf("dependency %q: %w", name, err)
		}
		if !present {
			return "", fmt.Errorf("dependency %q (%s) is not in the package store — run `fern -fetch %s` before vendoring", name, d.Hash, filepath.Join(m.Dir, manifest.FileName))
		}
		return store, nil
	}
	dir, _ := m.DepDir(name)
	return dir, nil
}

// copyPackage copies a package's Fern sources into dst: fern.toml plus
// every .fern / .fern.md file, preserving subdirectory layout. A nested
// `vendor/` directory is skipped (the flat top-level vendor is the only
// one that matters) as are dot-directories (.git and friends). Anything
// that isn't a source file is left behind — a vendored package is
// source, not a working tree.
func copyPackage(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if de.IsDir() {
			base := de.Name()
			if rel != "." && (base == "vendor" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		name := de.Name()
		if name != manifest.FileName && !strings.HasSuffix(name, ".fern") && !strings.HasSuffix(name, ".fern.md") {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
