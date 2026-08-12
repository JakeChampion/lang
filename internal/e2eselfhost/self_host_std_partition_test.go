package e2eselfhost

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/stdlib"
)

// TestSelfHostStdPartitionAgreesWithNative derives every `std/` module's host
// capability reach TWICE — once with native's platforms.Reach over
// modload.LoadStdlibFlat, once with the self-host's platforms.reach over its
// own modloader + flatten.bundle — and requires the same answer
// (examples/self_host/platforms_reach_run.fern, #6633 item 3).
//
// internal/platforms/std_partition_test.go already derives the partition on
// the native side and checks a checked-in table against it. This is the same
// derivation on the self-host side, and it is where the value is for "the
// self-host is the only compiler": the partition decides which modules a
// freestanding target can use, so the two compilers disagreeing about it means
// they disagree about what runs on a machine with no host.
//
// Fifty-odd real stdlib modules is also far more exercise than the capability
// scan gets anywhere else — the 8-case differential and the conformance sweep
// are mostly programs that call no gated builtin at all.
//
// It runs UN-SHAKEN on both sides, which is the whole point: reach is a
// property of a module's own code, and shaking first would answer for one
// importing program's call graph and classify every unused module as
// core-safe.
func TestSelfHostStdPartitionAgreesWithNative(t *testing.T) {
	if testing.Short() {
		t.Skip("std partition sweep builds a self-host driver; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("platforms_reach_run reads its entry off argv; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "platforms_reach_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "platforms_reach_run.fern", "platforms_reach_run")

	// The driver resolves `import "std/x"` as `<entry dir>/std/x.fern`, the
	// same path shape native's loader uses, so the embedded stdlib is written
	// out with its directories intact rather than flattened — `std/` and
	// `core/` both carry a `_test_empty.fern`, and a flat copy would silently
	// resolve one import to the other module's source.
	work := t.TempDir()
	writeEmbeddedStdlib(t, work)

	mods := stdModuleNames(t)
	if len(mods) < 40 {
		t.Fatalf("found %d std/ modules, expected the full set — a shrunken sweep proves nothing", len(mods))
	}

	var diffs []string
	hosted := 0
	for _, mod := range mods {
		t.Run(mod, func(t *testing.T) {
			prog, err := modload.LoadStdlibFlat([]string{mod})
			if err != nil {
				t.Fatalf("native cannot load %s, so it cannot be classified: %v", mod, err)
			}
			var caps []string
			for c := range platforms.Reach(prog) {
				caps = append(caps, c)
			}
			sort.Strings(caps)
			want := strings.Join(caps, ",")

			entry := filepath.Join(work, "entry_"+strings.ReplaceAll(mod, "/", "_")+".fern")
			src := "import \"" + mod + "\";\nfunction main(): i32 { return 0; }\n"
			if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			out, _ := exec.Command(bin, entry).Output()
			got := strings.Join(nonEmptyLines(string(out)), ",")
			if got != "" {
				hosted++
			}

			if got != want {
				diffs = append(diffs, mod+": native ["+want+"], self-host ["+got+"]")
				t.Errorf("reach for %s: native %q, self-host %q\n"+
					"The two compilers disagree about which host capabilities this module touches, "+
					"which is a disagreement about whether it runs on a freestanding target.", mod, want, got)
			}
		})
	}
	if len(diffs) > 0 {
		t.Logf("%d of %d std/ modules disagree", len(diffs), len(mods))
	}
	// A driver that failed to run at all prints nothing, which agrees with
	// every core-safe module and only disagrees with the hosted ones. Holding
	// a floor on how many capabilities the self-host actually REPORTED is what
	// separates "the two derivations agree" from "neither derived anything".
	if hosted < 10 {
		t.Errorf("the self-host reported a non-empty reach for only %d of %d std/ modules; "+
			"the partition has far more hosted modules than that, so the driver is not being read", hosted, len(mods))
	}
}

// stdModuleNames lists the embedded `std/` modules, as import paths.
func stdModuleNames(t *testing.T) []string {
	t.Helper()
	ents, err := fs.ReadDir(stdlib.FS(), "std")
	if err != nil {
		t.Fatalf("read embedded std/: %v", err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".fern") {
			continue
		}
		out = append(out, "std/"+strings.TrimSuffix(e.Name(), ".fern"))
	}
	sort.Strings(out)
	return out
}

// writeEmbeddedStdlib materialises the embedded stdlib under dir, keeping its
// directory structure so `std/x` and `core/x` stay distinct paths.
func writeEmbeddedStdlib(t *testing.T, dir string) {
	t.Helper()
	err := fs.WalkDir(stdlib.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, p), 0o755)
		}
		if !strings.HasSuffix(p, ".fern") {
			return nil
		}
		src, rerr := fs.ReadFile(stdlib.FS(), p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dir, p), src, 0o644)
	})
	if err != nil {
		t.Fatalf("materialise embedded stdlib: %v", err)
	}
}

// nonEmptyLines splits driver output into its non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
