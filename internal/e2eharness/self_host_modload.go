// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_modload_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// WriteSelfHostModloadProject lays out the self-host sources needed to
// build the import-driven driver (asm_modload_run.fern): the asm pipeline
// plus flatten + the real builtins module + the driver itself.
func WriteSelfHostModloadProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// treeshake backs the over-budget per-module rescue: the driver derives
	// the reachable-name set from it before pruning each unit.
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifyprovided.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern", "asm_modload_run.fern", "treeshake.fern", "rundriver.fern")
	return dir
}

// WriteSelfHostModloadProjectTyped is WriteSelfHostModloadProject for a test
// whose driver compiles the compiler's own sources over the typed lowering,
// which reads the stdlib modules' bodies.
func WriteSelfHostModloadProjectTyped(t *testing.T) string {
	t.Helper()
	dir := WriteSelfHostModloadProject(t)
	VendorStdlibImports(t, dir)
	return dir
}

var stdlibImport = regexp.MustCompile(`(?m)^import "((?:core|std)/[a-z0-9_/]+)";`)

// VendorStdlibImports copies every stdlib module the sources in dir import to
// dir/<path>.fern, where asm_modload_run's loader resolves `import "core/map"`.
// The typed lowering reads those modules' bodies; nothing else supplies them.
func VendorStdlibImports(t *testing.T, dir string) {
	t.Helper()
	root := SelfHostStdlibRoot(t)
	var queue []string
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".fern" {
			queue = append(queue, filepath.Join(dir, e.Name()))
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		src, err := os.ReadFile(queue[0])
		queue = queue[1:]
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stdlibImport.FindAllStringSubmatch(string(src), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			body, err := os.ReadFile(filepath.Join(root, m[1]+".fern"))
			if err != nil {
				t.Fatalf("vendor %s: %v", m[1], err)
			}
			dst := filepath.Join(dir, m[1]+".fern")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, body, 0o644); err != nil {
				t.Fatal(err)
			}
			queue = append(queue, dst)
		}
	}
}

// RunDriverFile runs the compiled driver binary with `entry` as argv[1]
// (plus any extra driver flags, e.g. "-target", "arm64-linux") and returns its
// stdout (the emitted asm).
func RunDriverFile(t *testing.T, runner []string, bin, entry string, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, append([]string{entry}, extraArgs...)...)
	out, err := cmd.Output()
	if err != nil {
		// The driver reports a refusal or a diagnostic on stderr, which
		// Output() captures but does not put in the error. Without it a
		// compile failure reads only as "exit status 1".
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("run driver on %s: %v\n%s", entry, err, stderr)
	}
	return out
}
