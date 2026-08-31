package e2eharness

// Benchmarks for the two in-process whole-program assemblers (#7904), so
// layout/table/parallelism decisions are argued from numbers. Each target
// assembles asm its own Go backend emitted, through the same entry point
// cmd/fern's link path uses (AssembleProgramWX at TextVAddrWX). Corpora are
// generated lazily inside the benchmark — a plain `go test` never pays for
// them — and the full-corpus emit (the whole self-host compiler, fern.fern)
// additionally skips under -short.
//
//	go test -run '^$' -bench BenchmarkAssemble ./internal/e2eharness

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

type asmBenchTarget struct {
	name     string
	emit     func(*ast.Program, *checker.Info) (string, error)
	assemble func(src string) error
}

var (
	asmBenchArm64 = asmBenchTarget{
		name: "arm64",
		emit: arm64codegen.Emit,
		assemble: func(src string) error {
			_, _, err := nativearm64.AssembleProgramWX(src, nativeelf.TextVAddrWX)
			return err
		},
	}
	asmBenchX86 = asmBenchTarget{
		name: "x86_64",
		emit: x86_64.Emit,
		assemble: func(src string) error {
			_, _, err := nativex86.AssembleProgramWX(src, nativeelf.TextVAddrWX)
			return err
		},
	}
)

// asmBenchSmall is a single trivial function: the emitted asm is dominated by
// the fixed program scaffolding (_start, the runtime helpers main reaches), so
// this sub-benchmark localises regressions in per-program overhead.
const asmBenchSmall = `function main(): i32 { return 40 + 2; }
`

// asmBenchMedium touches a spread of the runtime — refcounted arrays, string
// concat/slicing, f64, i64 division, a closure — without any imports, the same
// spread the whole-program encoding gate uses. A few thousand asm lines.
const asmBenchMedium = `
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function double(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 8) { xs = xs.append(fib(i)); i = i + 1; }
    var rows: i32[][] = [];
    rows = rows.append(xs);
    var s: string = "";
    var j: i32 = 0;
    while (j < xs.len()) { s = s + "ab"; j = j + 1; }
    var mid: string = slice_unchecked(s, 2, 6) + "";
    if (mid.len() != 4 || mid == "zzzz") { return 2; }
    var f: f64 = 3.0;
    var g: f64 = f * f + 4.0;
    var h: f64 = g / 2.0 - 1.5;
    var big: i64 = 1234567890123;
    var q: i64 = big / 1000;
    var r: i64 = big - q * 1000;
    var doubled: i32 = apply(double, xs[5]);
    if (h < 0.0 || r != 123 || doubled < 0) { return 3; }
    if (g > 12.5 && s.len() == 16 && rows[0].len() == 8) { return xs[7]; }
    return 1;
}
`

// asmBenchCorpora caches each generated corpus for the life of the test
// process, so the sizes of one target share a single emit per corpus.
var asmBenchCorpora = newBuildCache[string]()

// asmBenchCorpus returns the asm text for one (target, size) cell, emitting it
// on first use. "full" is the whole self-host compiler (fern.fern) — the
// largest real corpus the tree has — and its emit costs minutes and GBs, so it
// runs under the same memory reservation and soft heap cap as the harness's
// driver builds, and is skipped under -short.
func asmBenchCorpus(b *testing.B, tgt asmBenchTarget, size string) string {
	b.Helper()
	if size == "full" && testing.Short() {
		b.Skip("full corpus (fern.fern emit) skipped in -short")
	}
	asm, err := asmBenchCorpora.get(tgt.name+"/"+size, func() (string, error) {
		mainPath, err := asmBenchSourcePath(b, size)
		if err != nil {
			return "", err
		}
		var asm string
		emit := func() error {
			return withEmitMemLimit(func() error {
				var e error
				asm, e = asmBenchEmit(mainPath, tgt.emit)
				return e
			})
		}
		if size == "full" {
			err = withBuildMemory(heavyBuildWeightMB(), emit)
		} else {
			err = emit()
		}
		// Only the asm string survives; hand the emit's working set (AST,
		// IR, checker tables) back to the OS before the assembler's peak.
		debug.FreeOSMemory()
		return asm, err
	})
	if err != nil {
		b.Fatal(err)
	}
	return asm
}

// asmBenchSourcePath resolves a corpus size to a `.fern` entry file.
func asmBenchSourcePath(b *testing.B, size string) (string, error) {
	switch size {
	case "small", "medium":
		src := asmBenchSmall
		if size == "medium" {
			src = asmBenchMedium
		}
		p := filepath.Join(b.TempDir(), "main.fern")
		return p, os.WriteFile(p, []byte(src), 0o644)
	case "full":
		return filepath.Join(selfHostSrcDir, "fern.fern"), nil
	}
	panic("unknown corpus size " + size)
}

// asmBenchEmit runs the front of the pipeline (modload → constfold → check →
// monomorph) and the given backend, mirroring emitDriverAsm for either target.
func asmBenchEmit(mainPath string, emit func(*ast.Program, *checker.Info) (string, error)) (string, error) {
	prog, _, err := modload.Load(mainPath)
	if err != nil {
		return "", err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return "", err
	}
	info, err := checker.Check(prog)
	if err != nil {
		return "", err
	}
	if err := monomorph.Run(prog, info); err != nil {
		return "", err
	}
	return emit(prog, info)
}

func benchmarkAssemble(b *testing.B, tgt asmBenchTarget) {
	for _, size := range []string{"small", "medium", "full"} {
		b.Run(size, func(b *testing.B) {
			src := asmBenchCorpus(b, tgt, size)
			lines := strings.Count(src, "\n")
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := tgt.assemble(src); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if s := b.Elapsed().Seconds(); s > 0 {
				b.ReportMetric(float64(lines)*float64(b.N)/s, "lines/s")
			}
		})
	}
}

func BenchmarkAssembleArm64(b *testing.B)  { benchmarkAssemble(b, asmBenchArm64) }
func BenchmarkAssembleX86_64(b *testing.B) { benchmarkAssemble(b, asmBenchX86) }
