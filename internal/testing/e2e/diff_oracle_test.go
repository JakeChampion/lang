package e2e

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

func diffOracleShard(t *testing.T) (uint64, uint64) {
	t.Helper()
	raw := os.Getenv("DIFF_ORACLE_SHARD")
	if raw == "" {
		return 0, 1
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: want I/N (e.g. 0/4)", raw)
	}
	idx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: index parse: %v", raw, err)
	}
	count, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: count parse: %v", raw, err)
	}
	if count == 0 || idx >= count {
		t.Fatalf("DIFF_ORACLE_SHARD=%q: require 0 <= I < N and N > 0", raw)
	}
	return idx, count
}

func diffOracleSeedBase(t *testing.T) uint64 {
	t.Helper()
	raw := os.Getenv("DIFF_ORACLE_SEED_BASE")
	if raw == "" {
		return 0
	}
	base, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		t.Fatalf("DIFF_ORACLE_SEED_BASE=%q: parse: %v", raw, err)
	}
	return base
}

func diffOracleWindow(t *testing.T, count uint64) []uint64 {
	t.Helper()
	base := diffOracleSeedBase(t)
	shardIdx, shardCount := diffOracleShard(t)
	seeds := make([]uint64, 0, count/shardCount+1)
	for i := uint64(0); i < count; i++ {
		seed := base + i
		if seed%shardCount != shardIdx {
			continue
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

func FuzzGenerate_ExecutionAgrees(f *testing.F) {
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	e2eharness.WarmSelfHostCLI(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		src := fernsmith.GenMainBytes(data)
		expected := runInterpByteOrSkip(t, src)
		t.Run("arm64-linux", func(t *testing.T) {
			_, code := compileAndRunArm64(t, src)
			if code != expected {
				t.Errorf("arm64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("x86_64", func(t *testing.T) {
			_, code := compileAndRunX86_64(t, src)
			if code != expected {
				t.Errorf("x86_64 exit=%d, interp=%d\ndata=%x\nsrc:\n%s", code, expected, data, src)
			}
		})
		t.Run("wasm32-wasi", func(t *testing.T) {
			got := compileAndRunWasmbinMain(t, src)
			if got != expected {
				t.Errorf("wasm result=%d, interp=%d\ndata=%x\nsrc:\n%s", got, expected, data, src)
			}
		})
	})
}

func TestInterpOracleRunsGenericBodies(t *testing.T) {
	src := `import "core/cmp" as cmp;
function same[T: cmp.Eq](a: T, b: T): boolean { return a.eq(b); }
function main(): i32 {
    let r: i32 = 0;
    if (same(1, 1)) { r = r + 1; }
    if (!same(2, 3)) { r = r + 2; }
    if (same("a", "a")) { r = r + 4; }
    if (!same("a", "b")) { r = r + 8; }
    return r;
}`
	ran := false
	t.Run("Eq-bounded body", func(t *testing.T) {
		if got := runInterpByte(t, src); got != 15 {
			t.Errorf("interp: got exit %d, want 15", got)
		}
		ran = true
	})
	if !ran {
		t.Fatal("the interp oracle skipped a generic body: it is not running the differential suites' generic cases (#6840)")
	}
}

func runInterpByte(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, false)
}

func runInterpByteOrSkip(t *testing.T, src string) int {
	t.Helper()
	return interpByte(t, src, true)
}

func interpLoaded(t *testing.T, src string) *interp.Interp {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v\nsrc:\n%s", err, src)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v\nsrc:\n%s", err, src)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v\nsrc:\n%s", err, src)
	}
	i := interp.New()
	i.SetDynCoercions(info.DynCoercions)
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	return i
}

func interpByte(t *testing.T, src string, skipGaps bool) int {
	t.Helper()
	code, err := interpRunExit(t, src)
	if err != nil {
		if skipGaps {
			t.Skipf("interp coverage gap: %v", err)
		}
		t.Fatalf("interp: %v\nsrc:\n%s", err, src)
	}
	return code
}

func interpRunExit(t *testing.T, src string) (int, error) {
	t.Helper()
	i := interpLoaded(t, src)
	type exited struct{ code int }
	i.Exiter = func(code int) { panic(exited{code}) }
	var v interp.Value
	var err error
	exitCode := -1
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ie, ok := r.(exited); ok {
					exitCode = ie.code
					return
				}
				panic(r)
			}
		}()
		v, err = i.CallByName("main", nil)
	}()
	if exitCode >= 0 {
		return exitCode & 0xFF, nil
	}
	if err != nil {
		return 0, err
	}
	status, ok := interp.ExitStatus(v)
	if !ok {
		return 0, fmt.Errorf("main returned %T, want a number, a boolean or void", v)
	}
	return status, nil
}
