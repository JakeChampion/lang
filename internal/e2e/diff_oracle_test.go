// Differential-execution oracle. Generates Lang programs with
// `langsmith.GenMain` and runs each through every available
// backend, asserting they all agree on `main()`'s byte return
// value.
//
// The byte-mutation FuzzParse / FuzzCheck fuzzers and the
// langsmith parse-roundtrip fuzzer all stop at the front end;
// they catch parser / checker bugs but say nothing about IR
// lowering or codegen. This harness is the cross-backend
// counterpart: same source, four backends, one expected result.
// Any disagreement points at a real codegen bug.
//
// The interpreter is the source of truth and is always exercised
// (no toolchain). Each native / wasm backend runs in its own
// sub-test so it can skip individually when its toolchain is
// missing — the rest of the comparison still runs.
package e2e

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/langsmith"
	"github.com/jakechampion/lang/internal/parser"
)

// diffOracleSeedCount keeps `go test ./...` fast by default; the
// native FuzzGenerate_ExecutionAgrees harness in the same package
// expands the search space on demand.
const diffOracleSeedCount = 8

// TestDifferential_LangsmithMain runs each backend on the same
// generator-emitted main() and asserts the byte return value
// agrees across all available backends. Per-seed parent test
// gathers the interp baseline; per-backend child tests skip when
// the relevant toolchain is missing.
func TestDifferential_LangsmithMain(t *testing.T) {
	for seed := uint64(0); seed < diffOracleSeedCount; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			src := langsmith.GenMain(seed)
			expected := runInterpByte(t, src)

			t.Run("arm64", func(t *testing.T) {
				_, code := compileAndRunArm64(t, src)
				if code != expected {
					t.Errorf("arm64 exit=%d, interp=%d\nsrc:\n%s", code, expected, src)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				_, code := compileAndRunX86_64(t, src)
				if code != expected {
					t.Errorf("x86_64 exit=%d, interp=%d\nsrc:\n%s", code, expected, src)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				componentPath := buildComponent(t, src)
				stdout, stderr, ec := runComponent(t, componentPath, runOpts{})
				if ec != 0 {
					t.Fatalf("wasmtime exit=%d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
				}
				trimmed := strings.TrimSpace(stdout)
				got, err := strconv.Atoi(trimmed)
				if err != nil {
					t.Fatalf("parse wasm stdout %q: %v", trimmed, err)
				}
				if got != expected {
					t.Errorf("wasm result=%d, interp=%d\nsrc:\n%s", got, expected, src)
				}
			})
		})
	}
}

// FuzzGenerate_ExecutionAgrees is the same oracle as the table
// test, but for `go test -fuzz=...`. Operates on a uint64 seed
// per execution. The interp baseline still runs; backend
// disagreement (or any interp / parser / checker error) is a
// hard failure. Note that backends with missing toolchains will
// SKIP rather than fail, so this fuzzer only really stresses
// codegen when run on a machine with the full toolchain
// installed (or in CI).
//
// Run with: go test -fuzz=FuzzGenerate_ExecutionAgrees -run=^$ ./internal/e2e
func FuzzGenerate_ExecutionAgrees(f *testing.F) {
	// Seed with a handful of deterministic uint64 seeds repackaged
	// as 8-byte slices so the corpus is non-empty on first run;
	// after that the mutator drives the byte stream directly.
	for seed := uint64(0); seed < 16; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		src := langsmith.GenMainBytes(data)
		expected := runInterpByte(t, src)
		t.Run("arm64", func(t *testing.T) {
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
		t.Run("wasm", func(t *testing.T) {
			componentPath := buildComponent(t, src)
			stdout, stderr, ec := runComponent(t, componentPath, runOpts{})
			if ec != 0 {
				t.Fatalf("wasmtime exit=%d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
			}
			got, err := strconv.Atoi(strings.TrimSpace(stdout))
			if err != nil {
				t.Fatalf("parse wasm stdout: %v", err)
			}
			if got != expected {
				t.Errorf("wasm result=%d, interp=%d\ndata=%x\nsrc:\n%s", got, expected, data, src)
			}
		})
	})
}

// runInterpByte parses + checks + runs `main()` under the in-
// process interpreter and returns the result masked to a byte.
// Sources from `langsmith.GenMain` already mask to a byte; the
// extra `& 0xFF` here is defensive in case the harness is ever
// fed a program that doesn't.
func runInterpByte(t *testing.T, src string) int {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v\nsrc:\n%s", err, src)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	i := interp.New()
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("interp: %v\nsrc:\n%s", err, src)
	}
	n, ok := v.(interp.Number)
	if !ok {
		t.Fatalf("interp main returned non-number %T\nsrc:\n%s", v, src)
	}
	return int(int64(n) & 0xFF)
}
