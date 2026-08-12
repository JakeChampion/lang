package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMvsRules exercises the self-host's Minimum Version Selection
// (examples/self_host/mvs.fern, #6640) — the port of native's internal/mvs,
// and the first slice of the package-manager surface.
//
// The driver restates each internal/mvs test: the same index, the same
// demands, the same expected selection or the same error text. Exit 0 means
// every assertion held; a non-zero code identifies the case, so a regression
// names itself without a stdout diff.
func TestSelfHostMvsRules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("mvs_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "mvs_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "mvs_run.fern", "mvs_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("mvs_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("mvs_run exit code = %d, want 0 — that code is the failing assertion's id in mvs_run.fern", code)
	}
	if want := "mvs: every resolution rule agrees with native"; !strings.Contains(string(out), want) {
		t.Errorf("mvs_run stdout = %q, want it to contain %q", out, want)
	}
}

// writeResolveProject lays out a project for `-resolve`: a root manifest, the
// version index it names, and one directory per indexed package version
// carrying that version's own manifest. Every path in the index is relative
// to the index file, so the tree is relocatable and the two compilers are
// compared on trees that differ only in their root.
func writeResolveProject(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSelfHostResolveDifferentialX86_64 is the parity gate for `-resolve`:
// for the same project, native and the self-host must pin the same versions
// from the same sources, or refuse it for the same reason.
//
// This is the divergence shape that blocks "the self-host is the only
// compiler" — the lock file is what every later build reads, so two compilers
// resolving differently is two different programs from one source tree. The
// lock bytes are compared exactly, with the project root normalised away
// (each compiler runs over its own copy, and the lock records absolute
// directories).
func TestSelfHostResolveDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("resolve differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	const barLib = "[package]\nname = \"bar\"\n"
	index := "# registry\n" +
		"[foo]\n" +
		"\"1.0.0\" = { path = \"pkgs/foo-1.0.0\" }\n" +
		"\"1.1.0\" = { path = \"pkgs/foo-1.1.0\" }\n" +
		"[bar]\n" +
		"\"1.0.0\" = { path = \"pkgs/bar-1.0.0\" }\n" +
		"\"1.9.0\" = { path = \"./pkgs/bar-1.9.0\" }\n" +
		"\"2.0.0\" = { path = \"nested/../pkgs/bar-2.0.0\" }\n"
	pkgs := map[string]string{
		"index.toml":               index,
		"pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n",
		"pkgs/foo-1.1.0/fern.toml": "[package]\nname = \"foo\"\n\n[dependencies]\nbar = \"2.0.0\"\n",
		"pkgs/bar-1.0.0/fern.toml": barLib,
		"pkgs/bar-1.9.0/fern.toml": barLib,
		"pkgs/bar-2.0.0/fern.toml": barLib,
	}

	for _, c := range []struct {
		name     string
		manifest string
		wantOK   bool
	}{
		// MVS keeps the max of the minimums: foo@1.1.0 requires bar>=2.0.0,
		// so the root's bar>=1.0.0 is raised. The `.`/`..` segments in the
		// index paths are resolved the same way by both, which is what the
		// lock comparison pins.
		{"max-of-mins", "[package]\nname = \"app\"\nindex = \"index.toml\"\n\n[dependencies]\nfoo = \"1.1.0\"\nbar = \"1.0.0\"\n", true},
		// A top-level exclude rounds the demand up to the next higher
		// non-excluded indexed version.
		{"exclude-rounds-up", "[package]\nname = \"app\"\nindex = \"index.toml\"\n\n[dependencies]\nbar = \"1.0.0\"\n\n[exclude]\nbar = [\"1.0.0\", \"1.9.0\"]\n", true},
		// A demanded version absent from the index is a precise refusal,
		// never a silent round-up.
		{"absent-version", "[package]\nname = \"app\"\nindex = \"index.toml\"\n\n[dependencies]\nfoo = \"1.2.0\"\n", false},
		// Excluding every version at or above the demand is a refusal too.
		{"exclude-exhausted", "[package]\nname = \"app\"\nindex = \"index.toml\"\n\n[dependencies]\nfoo = \"1.1.0\"\n\n[exclude]\nfoo = [\"1.1.0\"]\n", false},
		// Versioned dependencies with no index to resolve them against.
		{"no-index", "[package]\nname = \"app\"\n\n[dependencies]\nfoo = \"1.0.0\"\n", false},
		// Nothing to resolve is a refusal, not an empty lock.
		{"no-version-deps", "[package]\nname = \"app\"\nindex = \"index.toml\"\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativeDir := filepath.Join(root, "native")
			selfDir := filepath.Join(root, "selfhost")
			for _, d := range []string{nativeDir, selfDir} {
				writeResolveProject(t, d, pkgs)
				writeResolveProject(t, d, map[string]string{"fern.toml": c.manifest})
			}

			nativeCmd := exec.Command(nativeBin, "-resolve", nativeDir)
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-resolve", selfDir)
			shOut, _ := shCmd.CombinedOutput()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native resolved = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native resolved = %v, self-host resolved = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
			// The message is compared for the refusals too: which dependency
			// is at fault, and why, is the whole content of a failed
			// resolution, and both compilers claim to say the same thing.
			if want, got := strings.ReplaceAll(string(nativeOut), nativeDir, "<ROOT>"),
				strings.ReplaceAll(string(shOut), selfDir, "<ROOT>"); want != got {
				t.Errorf("resolve output differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}
			if !c.wantOK {
				return
			}
			nativeLock, err := os.ReadFile(filepath.Join(nativeDir, "fern.lock"))
			if err != nil {
				t.Fatalf("native lock: %v", err)
			}
			shLock, err := os.ReadFile(filepath.Join(selfDir, "fern.lock"))
			if err != nil {
				t.Fatalf("self-host lock: %v", err)
			}
			want := strings.ReplaceAll(string(nativeLock), nativeDir, "<ROOT>")
			got := strings.ReplaceAll(string(shLock), selfDir, "<ROOT>")
			if want != got {
				t.Errorf("fern.lock differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}
		})
	}
}
