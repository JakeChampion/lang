package coreutils

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/platforms"
)

// The `long double` each Fern target has, which is the model
// coreutils/lib/ld must pick for it (#8513).
//
// GNU's printf, seq and numfmt convert in `long double`, so the parity
// corpus is only ever run against the format of the host it runs on —
// x86-64 on one CI lane, aarch64 on another. Nothing in that corpus can
// see a target it is not running on, and printf shipped an x87 model
// hardcoded for a year because the x86-64 lane was the only one that
// existed. This table is the part that does not depend on where the
// tests run: it names the format for every target, and the check below
// interprets the real selection once per target with target_arch() and
// target_os() folded for it.
//
// The numbers are the C compiler's, read off the target's own
// preprocessor (`clang --target=<t> -dM -E -x c /dev/null`, the
// __LDBL_MANT_DIG__ and __LDBL_MAX_EXP__ macros) and confirmed against
// aarch64 glibc's LDBL_MANT_DIG under qemu.
type ldFormat struct {
	mant     int
	emax     int
	explicit bool
}

var (
	x87       = ldFormat{mant: 64, emax: 16383, explicit: true}
	binary128 = ldFormat{mant: 113, emax: 16383, explicit: false}
	binary64  = ldFormat{mant: 53, emax: 1023, explicit: false}
)

// longDoubleFor is the rule stated over the two halves of a target name
// rather than over the names themselves, so a target added to
// internal/platforms is covered the moment its ISA and environment are
// ones already known — and fails loudly the moment either is not.
func longDoubleFor(isa, env string) (ldFormat, bool) {
	// Apple's ABI narrows the type to double whatever the ISA.
	if env == "darwin" {
		return binary64, true
	}
	switch isa {
	case "x86-64":
		return x87, true
	case "arm64", "wasm32":
		return binary128, true
	}
	return ldFormat{}, false
}

// TestLongDoubleModelPerTarget interprets coreutils/lib/ld's format()
// once for every target Fern compiles for. It runs on any host: the two
// target builtins fold from constfold's inputs, not from the machine,
// so the arm64-darwin answer is checked here as much as on a Mac.
func TestLongDoubleModelPerTarget(t *testing.T) {
	probe, err := filepath.Abs(filepath.Join("testdata", "ld_target", "main.fern"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for _, name := range platforms.Targets() {
		d := platforms.ForTarget(name)
		if d == nil {
			t.Fatalf("platforms.Targets() named %q, which ForTarget does not know", name)
		}
		want, ok := longDoubleFor(d.ISA, d.Environment)
		if !ok {
			t.Errorf("target %s (%s/%s) has no recorded long double: coreutils/lib/ld "+
				"models one format per target and cannot silently pick a fourth "+
				"(#8513). Establish what the C compiler gives `long double` there, "+
				"add it to lib/ld.fern's format() and to the rule above.",
				name, d.ISA, d.Environment)
			continue
		}
		t.Run(name, func(t *testing.T) {
			_, prog := e2eharness.LoadCheckMonoFor(t, probe, name)
			in := interp.New()
			for _, ed := range prog.Enums {
				in.RegisterEnum(ed)
			}
			for _, fn := range prog.Funcs {
				in.Register(fn)
			}
			num := func(fn string) int {
				v, err := in.CallByName(fn, nil)
				if err != nil {
					t.Fatalf("%s(): %v", fn, err)
				}
				n, ok := v.(interp.Number)
				if !ok {
					t.Fatalf("%s() returned %T, want a number", fn, v)
				}
				return int(int64(n))
			}
			got := ldFormat{mant: num("mant"), emax: num("emax"), explicit: num("explicit_bit") == 1}
			if got != want {
				t.Errorf("%s: long double is %+v, want %+v", name, got, want)
			}
		})
	}
}
