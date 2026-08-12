package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// vendorTree renders the file tree under dir as sorted `path\ncontent` records,
// so two vendored trees are compared as one string rather than entry by entry.
// Absent directory → "".
func vendorTree(t *testing.T, dir string) string {
	t.Helper()
	var rows []string
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rows = append(rows, filepath.ToSlash(rel)+"\n"+string(b))
		return nil
	})
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n---\n")
}

// TestSelfHostVendorDifferentialX86_64 is the parity gate for `-vendor`
// (#6640): over the same project, native and the self-host must write the same
// vendor tree — the same packages, under the same names, containing the same
// files — or refuse for the same reason.
//
// The tree is what makes this stricter than the earlier slices' output
// comparison. Vendoring is the step that makes a build offline, so a self-host
// that copies one file fewer produces a tree that resolves under native and
// not under itself, which is precisely the divergence that blocks "the
// self-host is the only compiler".
func TestSelfHostVendorDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("vendor differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	const lib = "pub function twelve(): i32 { return 12; }\n"

	for _, c := range []struct {
		name  string
		files map[string]string
		// target is the path passed to -vendor, relative to the project root.
		target string
		// cache, when set, is laid out under the project root and exported as
		// $FERN_CACHE_DIR — the content-addressed store a url dep copies from.
		cache  string
		wantOK bool
		// compareOutput is false where native's own message is not
		// deterministic; the exit code and the tree are still compared.
		compareOutput bool
	}{
		// The whole transitive graph flattens into one vendor/, keyed by each
		// package's manifest name rather than the dependency key that reached
		// it.
		{"path-transitive", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelp = { path = \"../helper\" }\n",
			"main.fern": "import \"help\";\nfunction main(): i32 { return help.twelve(); }\n",
			"../helper/fern.toml": "[package]\nname = \"helper\"\n" +
				"[dependencies]\ntextkit = { path = \"../textkit\" }\n",
			"../helper/lib.fern":   "import \"textkit\";\npub function twelve(): i32 { return textkit.twelve(); }\n",
			"../textkit/fern.toml": "[package]\nname = \"textkit\"\n",
			"../textkit/lib.fern":  lib,
		}, ".", "", true, true},
		// A vendored package is source: subdirectories and .fern.md documents
		// come along, everything else stays behind, and a dependency's own
		// vendor/ and dot-directories are not copied into the flat tree.
		{"sources-only", map[string]string{
			"fern.toml":               "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"main.fern":               "function main(): i32 { return 0; }\n",
			"../helper/fern.toml":     "[package]\nname = \"helper\"\n",
			"../helper/lib.fern":      lib,
			"../helper/doc.fern.md":   "# a literate module\n\n```fern\npub function d(): i32 { return 1; }\n```\n",
			"../helper/README.md":     "not a source file\n",
			"../helper/build.sh":      "#!/bin/sh\n",
			"../helper/sub/deep.fern": "pub function deep(): i32 { return 2; }\n",
			"../helper/sub/notes.txt": "ignored\n",
			// A nested vendor/ is not flattened into the top-level one.
			"../helper/vendor/other/fern.toml": "[package]\nname = \"other\"\n",
			"../helper/vendor/other/lib.fern":  lib,
			// Dot-directories are working-tree noise.
			"../helper/.git/config":  "[core]\n",
			"../helper/.hidden.fern": "pub function h(): i32 { return 3; }\n",
		}, ".", "", true, true},
		// A workspace root vendors the UNION of its members' external deps,
		// and skips the `workspace = true` deps that resolve within the tree.
		{"workspace-union", map[string]string{
			"fern.toml": "[workspace]\nmembers = [\"a\", \"b\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n" +
				"[dependencies]\nhelper = { path = \"../../helper\" }\nb = { workspace = true }\n",
			"a/lib.fern": lib,
			"b/fern.toml": "[package]\nname = \"b\"\n" +
				"[dependencies]\ntextkit = { path = \"../../textkit\" }\n",
			"b/lib.fern":           lib,
			"../helper/fern.toml":  "[package]\nname = \"helper\"\n",
			"../helper/lib.fern":   lib,
			"../textkit/fern.toml": "[package]\nname = \"textkit\"\n",
			"../textkit/lib.fern":  lib,
		}, ".", "", true, true},
		// A stale vendor/ is replaced, not merged into: a package dropped from
		// the manifest must not survive in the tree.
		{"stale-vendor-cleared", map[string]string{
			"fern.toml":                "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"main.fern":                "function main(): i32 { return 0; }\n",
			"vendor/removed/fern.toml": "[package]\nname = \"removed\"\n",
			"vendor/removed/lib.fern":  lib,
			"../helper/fern.toml":      "[package]\nname = \"helper\"\n",
			"../helper/lib.fern":       lib,
		}, ".", "", true, true},
		// A dependency with no manifest of its own is vendored under the
		// dependency key, and is not walked for deps it cannot declare.
		{"dep-without-manifest", map[string]string{
			"fern.toml":          "[package]\nname = \"app\"\n[dependencies]\nbare = { path = \"../bare\" }\n",
			"main.fern":          "function main(): i32 { return 0; }\n",
			"../bare/lib.fern":   lib,
			"../bare/extra.fern": "pub function e(): i32 { return 1; }\n",
		}, ".", "", true, true},
		// A file inside the package resolves upward to the package it belongs
		// to, as does a subdirectory.
		{"file-argument-resolves-upward", map[string]string{
			"fern.toml":           "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"main.fern":           "function main(): i32 { return 0; }\n",
			"../helper/fern.toml": "[package]\nname = \"helper\"\n",
			"../helper/lib.fern":  lib,
		}, "main.fern", "", true, true},
		// A versioned dependency has no directory of its own: its source is
		// whatever fern.lock pinned, exactly as at load time.
		{"versioned-through-lock", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\nindex = \"../index.toml\"\n" +
				"[dependencies]\nlibv = \"1.0.0\"\n",
			"main.fern": "function main(): i32 { return 0; }\n",
			"fern.lock": "# generated by `fern -resolve`; do not edit by hand\n\n" +
				"[[package]]\nname = \"libv\"\nversion = \"1.0.0\"\npath = \"@ROOT@/../libv-1.0.0\"\n",
			"../libv-1.0.0/fern.toml": "[package]\nname = \"libv\"\n",
			"../libv-1.0.0/lib.fern":  lib,
		}, ".", "", true, true},
		// …and without a lock it is refused, naming the command that writes
		// one — rather than silently vendoring the package into itself.
		{"versioned-without-lock", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\nindex = \"../index.toml\"\n" +
				"[dependencies]\nlibv = \"1.0.0\"\n",
			"main.fern": "function main(): i32 { return 0; }\n",
		}, ".", "", false, true},
		// A url dependency is copied FROM the content-addressed store —
		// vendoring never downloads.
		{"url-from-store", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\n[dependencies]\n" +
				"remote = { url = \"https://example.invalid/remote.tar.gz\", hash = \"sha256:" + storeHex + "\" }\n",
			"main.fern":                             "function main(): i32 { return 0; }\n",
			"cache/pkgs/" + storeHex + "/fern.toml": "[package]\nname = \"remote\"\n",
			"cache/pkgs/" + storeHex + "/lib.fern":  lib,
		}, ".", "cache", true, true},
		// A url dependency absent from the store is refused, naming `-fetch`.
		{"url-not-in-store", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\n[dependencies]\n" +
				"remote = { url = \"https://example.invalid/remote.tar.gz\", hash = \"sha256:" + storeHex + "\" }\n",
			"main.fern": "function main(): i32 { return 0; }\n",
		}, ".", "cache", false, true},
		// A directory no manifest governs is refused by both.
		{"no-manifest", map[string]string{
			"keep.txt": "",
		}, ".", "", false, true},
		// A workspace listing a member that is not a package is refused
		// rather than half-vendored.
		{"workspace-member-without-manifest", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"gone\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  lib,
			"gone/x.fern": lib,
		}, ".", "", false, true},
		// Two distinct packages claiming one name cannot share the flat
		// namespace. Native names the two directories in map order, so only
		// the refusal itself is compared.
		{"name-collision", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\n[dependencies]\n" +
				"a = { path = \"../a\" }\nb = { path = \"../b\" }\n",
			"main.fern":         "function main(): i32 { return 0; }\n",
			"../a/fern.toml":    "[package]\nname = \"a\"\n[dependencies]\ndup = { path = \"../dupa\" }\n",
			"../a/lib.fern":     lib,
			"../b/fern.toml":    "[package]\nname = \"b\"\n[dependencies]\ndup = { path = \"../dupb\" }\n",
			"../b/lib.fern":     lib,
			"../dupa/fern.toml": "[package]\nname = \"clash\"\n",
			"../dupa/lib.fern":  lib,
			"../dupb/fern.toml": "[package]\nname = \"clash\"\n",
			"../dupb/lib.fern":  lib,
		}, ".", "", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			// Each compiler runs over its own copy of the tree. `pkg` is the
			// project root the -vendor argument points into; siblings written
			// as "../x" land beside it, which is what a `path = "../x"` dep
			// resolves to.
			nativePkg := filepath.Join(root, "native", "pkg")
			selfPkg := filepath.Join(root, "selfhost", "pkg")
			for _, pkg := range []string{nativePkg, selfPkg} {
				files := map[string]string{}
				for rel, src := range c.files {
					files[rel] = strings.ReplaceAll(src, "@ROOT@", filepath.ToSlash(pkg))
				}
				writeResolveProject(t, pkg, files)
			}

			run := func(bin, pkg string) (string, bool) {
				cmd := exec.Command(bin, "-vendor", filepath.Join(pkg, c.target))
				if c.cache != "" {
					cmd.Env = append(os.Environ(), "FERN_CACHE_DIR="+filepath.Join(pkg, c.cache))
				}
				out, _ := cmd.CombinedOutput()
				return string(out), cmd.ProcessState.ExitCode() == 0
			}
			nativeOut, nativeOK := run(nativeBin, nativePkg)
			shOut, shOK := run(driverBin, selfPkg)

			if nativeOK != c.wantOK {
				t.Fatalf("native vendor ok = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native vendor ok = %v, self-host = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
			if c.compareOutput {
				want := strings.ReplaceAll(nativeOut, nativePkg, "<ROOT>")
				got := strings.ReplaceAll(shOut, selfPkg, "<ROOT>")
				if want != got {
					t.Errorf("vendor output differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
				}
			}
			want := vendorTree(t, filepath.Join(nativePkg, "vendor"))
			got := vendorTree(t, filepath.Join(selfPkg, "vendor"))
			if want != got {
				t.Errorf("vendor tree differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}
			// A refused vendor must not leave a half-written tree behind.
			if !c.wantOK && got != "" {
				t.Errorf("refused vendor still wrote a tree:\n%s", got)
			}
		})
	}
}

// storeHex is a syntactically valid sha256 hex digest. Nothing verifies it
// here — vendoring copies out of an already-populated store, and populating it
// (with the verification) is `fern -fetch`.
const storeHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestSelfHostVendoredLoadIsOffline is the property the tree comparison exists
// to protect: after the SELF-HOST vendors a project, the self-host compiles it
// with the original dependency directories deleted.
//
// The differential proves the two compilers write the same tree; this proves
// that tree is the offline artefact it claims to be, on the compiler that
// wrote it.
//
// One level deep on purpose. The self-host loader resolves every import
// against the ENTRY package's manifest (#6756), so a dependency's own
// dependencies do not load — vendored or not. Vendoring flattens the
// transitive graph correctly either way, which is what the differential's
// `path-transitive` case covers.
func TestSelfHostVendoredLoadIsOffline(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("vendored-load check runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	root := t.TempDir()
	pkg := filepath.Join(root, "app")
	writeResolveProject(t, pkg, map[string]string{
		"fern.toml":           "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
		"main.fern":           "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
		"../helper/fern.toml": "[package]\nname = \"helper\"\n",
		"../helper/lib.fern":  "pub function twelve(): i32 { return 12; }\n",
	})

	if out, err := exec.Command(driverBin, "-vendor", pkg).CombinedOutput(); err != nil {
		t.Fatalf("self-host vendor: %v\n%s", err, out)
	}
	if err := os.RemoveAll(filepath.Join(root, "helper")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(driverBin, "-check", filepath.Join(pkg, "main.fern")).CombinedOutput()
	if err != nil {
		t.Fatalf("vendored load should be offline: %v\n%s", err, out)
	}
}
