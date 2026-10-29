package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	ix, err := loadIndex(indexPath, root.IndexKey)
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

// loadIndex reads the index and, when the manifest names an index_key,
// verifies the detached signature in PATH.sig over the same bytes first.
func loadIndex(path, key string) (*mvs.Index, error) {
	if key == "" {
		return mvs.LoadIndex(path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", path, err)
	}
	if err := verifyIndexSignature(path, body, key); err != nil {
		return nil, err
	}
	ix, err := mvs.ParseIndex(string(body))
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", path, err)
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		dir = filepath.Dir(path)
	}
	ix.Dir = dir
	return ix, nil
}

// verifyIndexSignature checks PATH.sig, 128 hex digits of an ed25519
// signature over the index bytes, against the manifest's index_key.
func verifyIndexSignature(path string, body []byte, key string) error {
	pub, err := hex.DecodeString(strings.TrimPrefix(key, "ed25519:"))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("index %s: index_key %w", path, manifest.ValidateIndexKey(key))
	}
	sigPath := path + ".sig"
	text, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("index %s: fern.toml names index_key but %s cannot be read", path, sigPath)
	}
	sig, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("index %s: must be 128 hex digits of the ed25519 signature over the index", sigPath)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
		return fmt.Errorf("index %s: the signature in %s does not verify against index_key", path, sigPath)
	}
	return nil
}
