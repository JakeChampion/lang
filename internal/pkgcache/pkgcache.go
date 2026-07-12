// Package pkgcache is the per-machine content-addressed package store
// (docs/PACKAGE-MANAGEMENT-SOTA.md, storage row): each hash-addressed
// dependency archive is verified against its manifest-declared
// `sha256:` hash and unpacked ONCE at
// `$FERN_CACHE_DIR|os.UserCacheDir()/fern/pkgs/<hex>/`; every project
// referencing that hash shares the unpacked tree. The hash is of the
// ARCHIVE BYTES (a .tar.gz), computed before anything is unpacked, so
// a mirror serving different bytes is rejected outright — the URL is a
// mirror hint, the hash is the identity. Fetching happens ONLY through
// the explicit `fern fetch` command; the compiler reads the cache and
// never touches the network (the no-build-time-network constraint).
package pkgcache

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Root returns the package-store root directory (not necessarily
// existing yet). FERN_CACHE_DIR overrides for tests and hermetic CI.
func Root() (string, error) {
	if d := os.Getenv("FERN_CACHE_DIR"); d != "" {
		return filepath.Join(d, "pkgs"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	return filepath.Join(base, "fern", "pkgs"), nil
}

// Dir returns the unpacked directory for a `sha256:<hex>` hash, and
// whether it is already present in the store.
func Dir(hash string) (string, bool, error) {
	hexpart, ok := strings.CutPrefix(hash, "sha256:")
	if !ok {
		return "", false, fmt.Errorf("unsupported hash %q (want sha256:…)", hash)
	}
	root, err := Root()
	if err != nil {
		return "", false, err
	}
	d := filepath.Join(root, hexpart)
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d, true, nil
	}
	return d, false, nil
}

// Fetch downloads the archive at url, verifies its bytes against hash,
// and unpacks it into the store. A no-op when the hash is already
// present (the url isn't even contacted — content-addressing means
// there is nothing new a server could say). Returns the unpacked dir.
func Fetch(url, hash string) (string, error) {
	dir, present, err := Dir(hash)
	if err != nil {
		return "", err
	}
	if present {
		return dir, nil
	}
	raw, err := download(url)
	if err != nil {
		return "", err
	}
	got := HashBytes(raw)
	if got != hash {
		return "", fmt.Errorf("fetch %s: hash mismatch — manifest declares %s, server sent %s; refusing to unpack", url, hash, got)
	}
	return dir, unpackInto(dir, raw)
}

// FetchUnverified downloads the archive at url, computes its sha256, and
// unpacks it into the store under that computed hash — the Zig-style
// "fetch and tell me the hash" flow behind `fern -add --url` (the user
// has no hash yet; `add` records the one this returns into the
// manifest, and every later `fern -fetch` verifies against it). Returns
// the `sha256:<hex>` hash and the unpacked directory.
func FetchUnverified(url string) (hash, dir string, err error) {
	raw, err := download(url)
	if err != nil {
		return "", "", err
	}
	hash = HashBytes(raw)
	dir, present, err := Dir(hash)
	if err != nil {
		return "", "", err
	}
	if present {
		return hash, dir, nil
	}
	return hash, dir, unpackInto(dir, raw)
}

// HashBytes returns the `sha256:<hex>` content hash of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// download fetches url's body, bounded to 64 MiB so a hostile mirror
// can't exhaust disk (a legitimate archive that large should be split).
func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}
	const maxArchive = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	if len(raw) > maxArchive {
		return nil, fmt.Errorf("fetch %s: archive exceeds %d bytes", url, maxArchive)
	}
	return raw, nil
}

// unpackInto unpacks verified .tar.gz bytes into dir, atomically: a
// temp sibling is populated then renamed, so a crash never leaves a
// half-unpacked tree the existence check would trust. When the archive
// holds exactly one top-level directory (the `tar czf pkg.tar.gz pkg/`
// convention) that level is stripped, so fern.toml/lib.fern land at the
// package root either way.
func unpackInto(dir string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".unpack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	gz, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("archive entry %q escapes the package directory", hdr.Name)
		}
		dst := filepath.Join(tmp, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			// Store-owned files are read-only-ish (0444 would break
			// re-unpack on some systems; 0644 with an immutable-by-
			// convention store matches Go's module cache stance).
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// Symlinks / devices / etc. have no place in a source
			// package and are how archive-based attacks escape roots.
			return fmt.Errorf("archive entry %q has unsupported type %d (only files and directories)", hdr.Name, hdr.Typeflag)
		}
	}
	src := tmp
	if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 && entries[0].IsDir() {
		src = filepath.Join(tmp, entries[0].Name())
	}
	if err := os.Rename(src, dir); err != nil {
		// Lost a race with a concurrent fetch of the same hash — fine,
		// content-addressing makes both winners identical.
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
