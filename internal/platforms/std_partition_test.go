package platforms

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/stdlib"
)

// stdModuleReach is the DERIVED partition of `std/` against the OS
// boundary (#6512): each module mapped to the host capabilities its
// transitive reach touches, with the empty string meaning core-safe.
//
// Nothing here is a judgement call — every entry is what
// TestStdPartitionIsDerivedNotAsserted computes from the source. It is
// checked in so that a module ACQUIRING a host dependency is a visible
// diff on this table rather than a surprise at someone's first
// freestanding build. Update it by reading the failure, not by guessing.
//
// Two entries worth knowing:
//
//   - `std/math` is hosted, not core. It reads as an "obviously core"
//     numerics module and is in fact the random-number module
//     (`random_int` over the platform CSPRNG). Derivation caught what a
//     hand-written classification would have got wrong.
//   - `std/test` is hosted on four capabilities, which is the answer to
//     the question the issue raises: a freestanding target cannot run
//     the in-language test runner today, and `log` (its output sink) is
//     only one of the four in the way.
var stdModuleReach = map[string]string{
	"std/_test_empty":   "",
	"std/ansi":          "",
	"std/array":         "",
	"std/async":         "now",
	"std/base32":        "",
	"std/base64":        "",
	"std/cli":           "env",
	"std/convert":       "",
	"std/crypto":        "",
	"std/csv":           "",
	"std/dotenv":        "",
	"std/error":         "",
	"std/fetch":         "env,log,now,proc,tcp",
	"std/float":         "",
	"std/format":        "",
	"std/fuzz":          "env,fs,log,now,random",
	"std/glob":          "",
	"std/headers":       "",
	"std/hex":           "",
	"std/http":          "",
	"std/i32":           "",
	"std/i64":           "",
	"std/io":            "fs,stdin",
	"std/io_buffered":   "",
	"std/jni":           "",
	"std/json":          "",
	"std/log":           "log",
	"std/math":          "random",
	"std/mock_platform": "",
	"std/num":           "",
	"std/option":        "",
	"std/path":          "",
	"std/peg":           "",
	"std/rand":          "random",
	"std/regex":         "",
	"std/result":        "",
	"std/semver":        "",
	"std/set":           "",
	"std/sim":           "now,random",
	"std/sort":          "",
	"std/strdist":       "",
	"std/stream":        "",
	"std/string":        "",
	"std/table":         "",
	"std/tcp":           "env,log,now,proc,tcp",
	"std/test":          "env,fs,log,now",
	"std/textwrap":      "",
	"std/time":          "now",
	"std/u32":           "",
	"std/u64":           "",
	"std/unicode":       "",
	"std/url":           "",
	"std/utf8":          "",
	"std/uuid":          "now,random",
}

// TestStdPartitionIsDerivedNotAsserted computes each `std/` module's host
// reach from its source and checks the table above against it. The table
// is the artifact; this is the derivation that owns it.
//
// It fails three ways, and each is the point: a module that does not load
// cannot be classified at all; a module missing from the table is one
// nobody has looked at; and a reach that differs from the table is a
// module whose relationship to the OS boundary just changed.
func TestStdPartitionIsDerivedNotAsserted(t *testing.T) {
	ents, err := fs.ReadDir(stdlib.FS(), "std")
	if err != nil {
		t.Fatalf("read embedded std/: %v", err)
	}
	found := map[string]bool{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".fern") {
			continue
		}
		mod := "std/" + strings.TrimSuffix(name, ".fern")
		found[mod] = true
		t.Run(mod, func(t *testing.T) {
			prog, err := modload.LoadStdlibFlat([]string{mod})
			if err != nil {
				t.Fatalf("cannot load %s, so it cannot be classified: %v", mod, err)
			}
			var caps []string
			for c := range Reach(prog) {
				caps = append(caps, c)
			}
			sort.Strings(caps)
			got := strings.Join(caps, ",")

			want, listed := stdModuleReach[mod]
			if !listed {
				t.Fatalf("%s is not in stdModuleReach; its derived reach is %q — add it", mod, got)
			}
			if got != want {
				t.Errorf("%s reach = %q, table says %q\n"+
					"A module changing which host capabilities it can reach is exactly what this table exists to surface. "+
					"If the change is intended, update the entry; if not, the import that pulled it in is the bug.",
					mod, got, want)
			}
		})
	}
	for mod := range stdModuleReach {
		if !found[mod] {
			t.Errorf("stdModuleReach lists %s, which no longer exists in std/ — drop it", mod)
		}
	}
}

// TestStdCoreSafeModulesCheckAgainstFreestanding is the end-to-end form of
// the claim: a module whose derived reach is empty must actually survive
// the freestanding capability gate. Reach and Enforce share one scan, so
// this pins that the partition means what the compiler enforces rather
// than agreeing with it by construction alone.
func TestStdCoreSafeModulesCheckAgainstFreestanding(t *testing.T) {
	for mod, reach := range stdModuleReach {
		if reach != "" {
			continue
		}
		t.Run(mod, func(t *testing.T) {
			prog, err := modload.LoadStdlibFlat([]string{mod})
			if err != nil {
				t.Fatalf("load %s: %v", mod, err)
			}
			if v := Enforce(prog, "freestanding"); len(v) != 0 {
				t.Errorf("%s is classified core-safe but E066s on freestanding: %s", mod, v[0].Message(""))
			}
		})
	}
}
