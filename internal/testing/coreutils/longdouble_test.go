package coreutils

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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

func longDoubleFor(isa, env string) (ldFormat, bool) {
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
