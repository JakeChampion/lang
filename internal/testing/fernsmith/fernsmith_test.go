package fernsmith_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

func sweepN(t *testing.T, full uint64) uint64 {
	t.Helper()
	if testing.Short() {
		short := full / 8
		if short < 4 {
			short = 4
		}
		return short
	}
	return full
}

func randBytes(r *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := 0; i+8 <= n; i += 8 {
		binary.LittleEndian.PutUint64(out[i:], r.Uint64())
	}
	if tail := n % 8; tail != 0 {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], r.Uint64())
		copy(out[n-tail:], buf[:tail])
	}
	return out
}

func checkGenerated(t *testing.T, src string) error {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}

func TestGenProducesValidPrograms(t *testing.T) {
	n := sweepN(t, 256)
	for seed := uint64(0); seed < n; seed++ {
		src := fernsmith.Gen(seed)
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("seed=%d failed to type-check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

func TestGenIsDeterministic(t *testing.T) {
	for _, seed := range []uint64{0, 1, 42, 1234567} {
		a := fernsmith.Gen(seed)
		b := fernsmith.Gen(seed)
		if a != b {
			t.Errorf("seed=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", seed, a, b)
		}
	}
}

func TestGenEmitsAtLeastOneFunction(t *testing.T) {
	for seed := uint64(0); seed < 16; seed++ {
		src := fernsmith.Gen(seed)
		if !strings.Contains(src, "function gen_f0(") {
			t.Errorf("seed=%d: missing gen_f0 in output\nsrc:\n%s", seed, src)
		}
	}
}

func TestGenMainProducesRunnablePrograms(t *testing.T) {
	n := sweepN(t, 256)
	for seed := uint64(0); seed < n; seed++ {
		src := fernsmith.GenMain(seed)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("seed=%d: missing main\nsrc:\n%s", seed, src)
			continue
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("seed=%d write: %v", seed, err)
		}
		prog, _, err := modload.Load(srcPath)
		if err != nil {
			t.Fatalf("seed=%d modload:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if err := constfold.Fold(prog, nil); err != nil {
			t.Fatalf("seed=%d constfold:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("seed=%d check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Fatalf("seed=%d monomorph:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

func TestGenBytesProducesValidPrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 99))
	n := sweepN(t, 64)
	for i := 0; uint64(i) < n; i++ {
		n := r.IntN(512)
		data := randBytes(r, n)
		src := fernsmith.GenBytes(data)
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("i=%d len=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
	}
}

func TestGenMainBytesProducesRunnablePrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 13))
	iters := sweepN(t, 64)
	for i := 0; uint64(i) < iters; i++ {
		n := r.IntN(512)
		data := randBytes(r, n)
		src := fernsmith.GenMainBytes(data)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("i=%d: missing main\nsrc:\n%s", i, src)
			continue
		}
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("i=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, src, err)
		}
	}
}

func TestGenBytesIsDeterministic(t *testing.T) {
	for _, n := range []int{0, 1, 8, 64, 256} {
		r := rand.New(rand.NewPCG(uint64(n), 31415))
		data := randBytes(r, n)
		a := fernsmith.GenBytes(data)
		b := fernsmith.GenBytes(data)
		if a != b {
			t.Errorf("len=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", n, a, b)
		}
	}
}

var (
	u32LiteralRE = regexp.MustCompile(`\b(\d+)u32\b`)
	u64LiteralRE = regexp.MustCompile(`\b(\d+)u64\b`)
	i64LiteralRE = regexp.MustCompile(`\b(\d+)i64\b`)
	u8LiteralRE  = regexp.MustCompile(`\b(\d+)u8\b`)
)

func unsignedOperandNear(src, suffix, op string, lit *regexp.Regexp) bool {
	for i := 0; i+len(op) <= len(src); i++ {
		if src[i:i+len(op)] != op {
			continue
		}
		left := strings.TrimRight(src[:i], "( )")
		if strings.HasSuffix(left, suffix) {
			return true
		}
		right := strings.TrimLeft(src[i+len(op):], "( ")
		if m := lit.FindStringIndex(right); m != nil && m[0] == 0 {
			return true
		}
	}
	return false
}

func u32OperandNear(src, op string) bool {
	return unsignedOperandNear(src, "u32", op, u32LiteralRE)
}

func u64OperandNear(src, op string) bool {
	return unsignedOperandNear(src, "u64", op, u64LiteralRE)
}

func wideOperandNear(src, op string) bool {
	return unsignedOperandNear(src, "i64", op, i64LiteralRE) ||
		unsignedOperandNear(src, "u64", op, u64LiteralRE)
}

var u32AsI32RE = regexp.MustCompile(`u32[^()]*\) as i32\)`)

func loopReadBeforeWithOrdered(src string) bool {
	for i := 0; i < 64; i++ {
		recv := fmt.Sprintf("__cowl_s%d", i)
		read := fmt.Sprintf("cwl%d_acc = cwl%d_acc + %s[", i, i, recv)
		with := recv + ".with("
		r := strings.Index(src, read)
		w := strings.Index(src, with)
		if r >= 0 && w > r {
			return true
		}
	}
	return false
}

func aliasCowClusterOrdered(src string) bool {
	for n := 0; strings.Contains(src, fmt.Sprintf("let __cow_s%d:", n)); n++ {
		alias := strings.Index(src, fmt.Sprintf("= __cow_s%d; ", n))
		write := strings.Index(src, fmt.Sprintf("__cow_s%d.with(", n))
		read := strings.Index(src, fmt.Sprintf("= __cow_a%d[", n))
		if alias >= 0 && write > alias && read > write {
			return true
		}
	}
	return false
}

func TestGenFeatureCoverage(t *testing.T) {
	want := map[string]bool{
		"function call":                false,
		"if-expression":                false,
		"binary arithmetic":            false,
		"variable reference":           false,
		"helper-call inside main":      false,
		"nested call (call as arg)":    false,
		"main with let declarations":   false,
		"while loop":                   false,
		"struct decl":                  false,
		"enum decl":                    false,
		"array literal":                false,
		"array index":                  false,
		"pair literal":                 false,
		"field access":                 false,
		"enum variant in expr":         false,
		"match expression":             false,
		"string concat":                false,
		"f-string":                     false,
		"len(string)":                  false,
		"pipe operator":                false,
		"Some literal":                 false,
		"None literal":                 false,
		"Option match":                 false,
		"try operator (?)":             false,
		"method call (.sum)":           false,
		"method call (.swap)":          false,
		"Xyz struct decl":              false,
		"Xyz literal":                  false,
		"Xyz.n field access":           false,
		"Xyz.valid field access":       false,
		"Status enum decl":             false,
		"Status variant":               false,
		"Status match expression":      false,
		"Map literal":                  false,
		"map get / contains / len":     false,
		"dynamic struct decl":          false,
		"dynamic enum decl":            false,
		"id[T] generic call":           false,
		"pick[T] generic call":         false,
		"for-each over array":          false,
		"for-each over map":            false,
		"nested function (closure)":    false,
		"Ok literal":                   false,
		"Err literal":                  false,
		"Result match-with-binding":    false,
		"Result try (?)":               false,
		"u32 let declaration":          false,
		"u32 literal above i32::MAX":   false,
		"unsigned comparison":          false,
		"unsigned divide or remainder": false,
		"unsigned shift":               false,
		"u32 reinterpreted as i32":     false,
		"u64 let declaration":          false,
		"u64 literal above i64::MAX":   false,
		"unsigned 64-bit comparison":   false,
		"unsigned 64-bit divide/rem":   false,
		"unsigned 64-bit shift":        false,
		"u64 high half as i32":         false,
		"saturating add or subtract":   false,
		"saturating multiply":          false,
		"saturating shift":             false,
		"saturating at i64 or u64":     false,
		"checked add or subtract":      false,
		"checked multiply":             false,
		"checked divide or remainder":  false,
		"checked shift":                false,
		"checked at i64 or u64":        false,
		"checked None arm taken":       false,
		"u8 let declaration":           false,
		"u8 literal at or above 200":   false,
		"u8 arithmetic":                false,
		"u8 saturating or checked":     false,
		"lambda literal":               false,
		"fn-typed var":                 false,
		"closure array literal":        false,
		"closure escapes via return":   false,
		"fn-typed parameter":           false,
		"indirect call via fn value":   false,
		"array .with() call":           false,
		"array alias of a bare ident":  false,
		"alias read after .with":       false,
		"loop read before .with":       false,
		"if/else statement":            false,
		"loop with break":              false,
		"match statement":              false,
		"labelled break":               false,
		"labelled continue":            false,
		"defer block":                  false,
		"match guard (when)":           false,
		"at-binding (@)":               false,
		"loop with continue":           false,
		"tuple destructure":            false,
		"if let Some":                  false,
		"if let Ok":                    false,
		"let Some … else":              false,
	}
	for seed := uint64(0); seed < 1024; seed++ {
		src := fernsmith.GenMain(seed)
		if strings.Contains(src, "gen_f0(") {
			want["function call"] = true
		}
		if strings.Contains(src, "(if (") {
			want["if-expression"] = true
		}
		if strings.Contains(src, " + ") || strings.Contains(src, " * ") {
			want["binary arithmetic"] = true
		}
		if strings.Contains(src, "v0") && strings.Count(src, "v0") > 1 {
			want["variable reference"] = true
		}
		if i := strings.Index(src, "function main"); i >= 0 {
			tail := src[i:]
			if strings.Contains(tail, "gen_f0(") || strings.Contains(tail, "gen_f1(") {
				want["helper-call inside main"] = true
			}
		}
		if strings.Count(src, "gen_f0(") >= 2 || strings.Count(src, "gen_f1(") >= 2 {
			want["nested call (call as arg)"] = true
		}
		if i := strings.Index(src, "function main"); i >= 0 && strings.Contains(src[i:], "let v0") {
			want["main with let declarations"] = true
		}
		if strings.Contains(src, "while (__loop_i") {
			want["while loop"] = true
		}
		if strings.Contains(src, "struct Pair { fst: i32, snd: i32 }") {
			want["struct decl"] = true
		}
		if strings.Contains(src, "enum Color { Red, Green, Blue }") {
			want["enum decl"] = true
		}
		if strings.Contains(src, "i32[] = [") || strings.Contains(src, "i64[] = [") || strings.Contains(src, "boolean[] = [") {
			want["array literal"] = true
		}
		if strings.Contains(src, "[0i32]") {
			want["array index"] = true
		}
		if strings.Contains(src, "(Pair { fst: ") {
			want["pair literal"] = true
		}
		if strings.Contains(src, ".fst") || strings.Contains(src, ".snd") {
			want["field access"] = true
		}
		if strings.Count(src, "Red") > 1 || strings.Count(src, "Green") > 1 || strings.Count(src, "Blue") > 1 {
			want["enum variant in expr"] = true
		}
		if strings.Contains(src, "match (") {
			want["match expression"] = true
		}
		if strings.Contains(src, "\" + \"") || strings.Contains(src, "\" + ") || strings.Contains(src, " + \"") {
			want["string concat"] = true
		}
		if strings.Contains(src, `f"`) {
			want["f-string"] = true
		}
		if strings.Contains(src, "len(") {
			want["len(string)"] = true
		}
		if strings.Contains(src, "|>") {
			want["pipe operator"] = true
		}
		if strings.Contains(src, "(Some(") {
			want["Some literal"] = true
		}
		for _, tok := range []string{" None;", " None,", " None ", "{None}", "= None;"} {
			if strings.Contains(src, tok) {
				want["None literal"] = true
				break
			}
		}
		if strings.Contains(src, "match (") && strings.Contains(src, "Some(__opt_x") {
			want["Option match"] = true
		}
		if strings.Contains(src, "?)") {
			want["try operator (?)"] = true
		}
		if strings.Contains(src, ".sum()") {
			want["method call (.sum)"] = true
		}
		if strings.Contains(src, ".swap()") {
			want["method call (.swap)"] = true
		}
		if strings.Contains(src, "struct Xyz { n: i32, valid: boolean }") {
			want["Xyz struct decl"] = true
		}
		if strings.Contains(src, "(Xyz { n: ") {
			want["Xyz literal"] = true
		}
		if strings.Contains(src, ".n ") || strings.Contains(src, ".n)") || strings.Contains(src, ".n,") || strings.Contains(src, ".n;") {
			want["Xyz.n field access"] = true
		}
		if strings.Contains(src, ".valid") {
			want["Xyz.valid field access"] = true
		}
		if strings.Contains(src, "enum Status { Active, Inactive, Pending }") {
			want["Status enum decl"] = true
		}
		if strings.Count(src, "Active") > 1 || strings.Count(src, "Inactive") > 1 || strings.Count(src, "Pending") > 1 {
			want["Status variant"] = true
		}
		if strings.Contains(src, "Active =>") {
			want["Status match expression"] = true
		}
		if strings.Contains(src, "(Map {") || strings.Contains(src, "Map[i32, i32]") {
			want["Map literal"] = true
		}
		if strings.Contains(src, ".get(") || strings.Contains(src, ".contains(") || strings.Contains(src, ".len()") {
			want["map get / contains / len"] = true
		}
		if strings.Contains(src, "struct S0 ") || strings.Contains(src, "struct S1 ") {
			want["dynamic struct decl"] = true
		}
		if strings.Contains(src, "enum E0 ") || strings.Contains(src, "enum E1 ") {
			want["dynamic enum decl"] = true
		}
		if strings.Count(src, "id(") > 1 {
			want["id[T] generic call"] = true
		}
		if strings.Count(src, "pick(") > 1 {
			want["pick[T] generic call"] = true
		}
		if strings.Contains(src, "for __fe_x") {
			want["for-each over array"] = true
		}
		if strings.Contains(src, "for (__fe_k") {
			want["for-each over map"] = true
		}
		if strings.Contains(src, "function __local_fn") {
			want["nested function (closure)"] = true
		}
		if strings.Contains(src, "; if (") || strings.Contains(src, "{ if (") {
			want["if/else statement"] = true
		}
		if strings.Contains(src, "loop { if (__lb") {
			want["loop with break"] = true
		}
		if strings.Contains(src, "match (") && strings.Contains(src, "(__ms") {
			want["match statement"] = true
		}
		if strings.Contains(src, "break __lp") {
			want["labelled break"] = true
		}
		if strings.Contains(src, "continue __lp") {
			want["labelled continue"] = true
		}
		if strings.Contains(src, "defer { ") {
			want["defer block"] = true
		}
		if strings.Contains(src, ") when (") {
			want["match guard (when)"] = true
		}
		if strings.Contains(src, " @ Some(") || strings.Contains(src, " @ Ok(") {
			want["at-binding (@)"] = true
		}
		if strings.Contains(src, "continue; }") {
			want["loop with continue"] = true
		}
		if strings.Contains(src, "let (__td") {
			want["tuple destructure"] = true
		}
		if strings.Contains(src, "if let Some(__il") {
			want["if let Some"] = true
		}
		if strings.Contains(src, "if let Ok(__il") {
			want["if let Ok"] = true
		}
		if strings.Contains(src, "let Some(__le") {
			want["let Some … else"] = true
		}
		if strings.Contains(src, "((__lam_x") {
			want["lambda literal"] = true
		}
		if strings.Contains(src, ": (i32) => i32 =") {
			want["fn-typed var"] = true
		}
		if strings.Contains(src, "((i32) => i32)[] = [") {
			want["closure array literal"] = true
		}
		if strings.Contains(src, "): (i32) => i32 {") {
			want["closure escapes via return"] = true
		}
		if strings.Contains(src, "p0: (i32) => i32") || strings.Contains(src, ", p1: (i32) => i32") {
			want["fn-typed parameter"] = true
		}
		if fnCallRE.MatchString(src) {
			want["indirect call via fn value"] = true
		}
		if strings.Contains(src, "(Ok(") {
			want["Ok literal"] = true
		}
		if strings.Contains(src, "(Err(") {
			want["Err literal"] = true
		}
		if strings.Contains(src, "Ok(__res_ok") {
			want["Result match-with-binding"] = true
		}
		if strings.Contains(src, "?)") && strings.Contains(src, "Result[i32, i32]") {
			want["Result try (?)"] = true
		}
		if strings.Contains(src, ": u32 ") {
			want["u32 let declaration"] = true
		}
		for _, m := range u32LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > math.MaxInt32 {
				want["u32 literal above i32::MAX"] = true
				break
			}
		}
		for _, op := range []string{" < ", " > ", " <= ", " >= "} {
			if u32OperandNear(src, op) {
				want["unsigned comparison"] = true
			}
		}
		for _, op := range []string{" / ", " % "} {
			if u32OperandNear(src, op) {
				want["unsigned divide or remainder"] = true
			}
		}
		if u32OperandNear(src, " >> ") || u32OperandNear(src, " << ") {
			want["unsigned shift"] = true
		}
		if u32AsI32RE.MatchString(src) {
			want["u32 reinterpreted as i32"] = true
		}
		if strings.Contains(src, ": u64 ") {
			want["u64 let declaration"] = true
		}
		for _, m := range u64LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > math.MaxInt64 {
				want["u64 literal above i64::MAX"] = true
				break
			}
		}
		for _, op := range []string{" < ", " > ", " <= ", " >= "} {
			if u64OperandNear(src, op) {
				want["unsigned 64-bit comparison"] = true
			}
		}
		for _, op := range []string{" / ", " % "} {
			if u64OperandNear(src, op) {
				want["unsigned 64-bit divide/rem"] = true
			}
		}
		if u64OperandNear(src, " >> ") || u64OperandNear(src, " << ") {
			want["unsigned 64-bit shift"] = true
		}
		if strings.Contains(src, ") >> 32u64) as i32)") {
			want["u64 high half as i32"] = true
		}
		if strings.Contains(src, " +| ") || strings.Contains(src, " -| ") {
			want["saturating add or subtract"] = true
		}
		if strings.Contains(src, " *| ") {
			want["saturating multiply"] = true
		}
		if strings.Contains(src, " <<| ") {
			want["saturating shift"] = true
		}
		for _, op := range []string{" +| ", " -| ", " *| ", " <<| "} {
			if wideOperandNear(src, op) {
				want["saturating at i64 or u64"] = true
			}
		}
		if strings.Contains(src, " +? ") || strings.Contains(src, " -? ") {
			want["checked add or subtract"] = true
		}
		if strings.Contains(src, " *? ") {
			want["checked multiply"] = true
		}
		if strings.Contains(src, " /? ") || strings.Contains(src, " %? ") {
			want["checked divide or remainder"] = true
		}
		if strings.Contains(src, " <<? ") || strings.Contains(src, " >>? ") {
			want["checked shift"] = true
		}
		for _, op := range []string{" +? ", " -? ", " *? ", " /? ", " %? ", " <<? ", " >>? "} {
			if wideOperandNear(src, op) {
				want["checked at i64 or u64"] = true
			}
		}
		if strings.Contains(src, "None => ") && strings.Contains(src, "__chk_v") {
			want["checked None arm taken"] = true
		}
		if strings.Contains(src, ": u8 ") {
			want["u8 let declaration"] = true
		}
		for _, m := range u8LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n >= 200 {
				want["u8 literal at or above 200"] = true
				break
			}
		}
		for _, op := range []string{" + ", " - ", " * ", " / ", " % ", " << ", " >> "} {
			if unsignedOperandNear(src, "u8", op, u8LiteralRE) {
				want["u8 arithmetic"] = true
			}
		}
		for _, op := range []string{" +| ", " -| ", " *| ", " <<| ", " +? ", " -? ", " *? ", " /? ", " %? ", " <<? ", " >>? "} {
			if unsignedOperandNear(src, "u8", op, u8LiteralRE) {
				want["u8 saturating or checked"] = true
			}
		}
		if strings.Contains(src, ".with(") {
			want["array .with() call"] = true
		}
		if strings.Contains(src, "= __cow_s") {
			want["array alias of a bare ident"] = true
		}
		if aliasCowClusterOrdered(src) {
			want["alias read after .with"] = true
		}
		if loopReadBeforeWithOrdered(src) {
			want["loop read before .with"] = true
		}
	}
	for feature, ok := range want {
		if !ok {
			t.Errorf("feature never seen in 1024 GenMain seeds: %s", feature)
		}
	}
}

func TestGenPrintableFloatCoverage(t *testing.T) {
	want := map[string]bool{
		"f32 literal":                   false,
		"f64 literal":                   false,
		"f64 let or param":              false,
		"f64 arithmetic":                false,
		"f64 truncated to i32":          false,
		"f64 literal past f32 mantissa": false,
	}
	for seed := uint64(0); seed < 1024; seed++ {
		src := fernsmith.GenPrintableMain(seed)
		if strings.Contains(src, "f32") {
			want["f32 literal"] = true
		}
		if f64LiteralRE.MatchString(src) {
			want["f64 literal"] = true
		}
		if strings.Contains(src, ": f64") {
			want["f64 let or param"] = true
		}
		for _, op := range []string{" + ", " - ", " * ", " / "} {
			if unsignedOperandNear(src, "f64", op, f64LiteralRE) {
				want["f64 arithmetic"] = true
			}
		}
		if strings.Contains(src, "f64) as i32)") || f64CastRE.MatchString(src) {
			want["f64 truncated to i32"] = true
		}
		for _, m := range f64LiteralRE.FindAllStringSubmatch(src, -1) {
			if len(strings.ReplaceAll(m[1], ".", "")) > 8 {
				want["f64 literal past f32 mantissa"] = true
				break
			}
		}
	}
	for feature, ok := range want {
		if !ok {
			t.Errorf("feature never seen in 1024 GenPrintableMain seeds: %s", feature)
		}
	}
}

var (
	f64LiteralRE = regexp.MustCompile(`\b(\d+\.\d+)f64\b`)
	f64CastRE    = regexp.MustCompile(`f64[^()]*\) as i32\)`)
	fnCallRE     = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:v\d+|p\d+|lp\d+|w\d+_\d+|__fe_x\d+|__lam_x\d+)\(`)
)

var reFloatAggDecl = regexp.MustCompile(`let (__fag\d+): (.+?) = `)

func floatAggObservation(mainBody string) (kind string, allSlotsPrinted bool) {
	m := reFloatAggDecl.FindStringSubmatch(mainBody)
	if m == nil {
		return "", false
	}
	name, kind := m[1], strings.TrimSpace(m[2])
	var want []string
	switch kind {
	case "f32[]", "f64[]":
		for i := 0; i < 3; i++ {
			want = append(want, fmt.Sprintf("print(((%s[%di32]) as i32)", name, i))
		}
	case "(f32, f64)":
		want = []string{
			fmt.Sprintf("print(((%s.0) as i32)", name),
			fmt.Sprintf("print(((%s.1) as i32)", name),
		}
	case "Vec2":
		want = []string{
			fmt.Sprintf("print(((%s.x) as i32)", name),
			fmt.Sprintf("print(((%s.y) as i32)", name),
		}
	case "FShape":
		idx := strings.TrimPrefix(name, "__fag")
		for slot, narrow := range []string{"a", "b"} {
			wide := []string{"c", "d"}[slot]
			want = append(want, fmt.Sprintf(
				"print((match (%s) { FNone => 0i32, FTwo(__fs_a%s_%d, __fs_b%s_%d) => ((__fs_%s%s_%d) as i32), FWide(__fs_c%s_%d, __fs_d%s_%d) => ((__fs_%s%s_%d) as i32) }).to_string());",
				name, idx, slot, idx, slot, narrow, idx, slot, idx, slot, idx, slot, wide, idx, slot))
		}
	default:
		return kind, false
	}
	for _, w := range want {
		if !strings.Contains(mainBody, w) {
			return kind, false
		}
	}
	return kind, true
}

func mainBodyOf(src string) string {
	if i := strings.Index(src, "function main"); i >= 0 {
		return src[i:]
	}
	return ""
}

var (
	reDynStructFloatField = regexp.MustCompile(`struct S\d+ \{[^}]*: f(?:32|64)`)
	reDynEnumFloatPayload = regexp.MustCompile(`__E\d+_V\d+\([^)]*f(?:32|64)`)
	reMainLocalDecl       = regexp.MustCompile(`let v\d+: (.+?) = `)
)

func TestGenPrintableFloatsReachAggregates(t *testing.T) {
	const seeds = 2048
	landmarks := []struct {
		name  string
		floor float64
		hit   func(src, body string) bool
	}{
		{"f32[] built and every element printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "f32[]" && ok
		}},
		{"f64[] built and every element printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "f64[]" && ok
		}},
		{"(f32, f64) built and both elements printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "(f32, f64)" && ok
		}},
		{"Vec2 built and both f32 fields printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "Vec2" && ok
		}},
		{"FShape multi-slot payload built and both slots printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "FShape" && ok
		}},
		{"f32[] as an ordinary main-level local", 0.02, mainLocalIs("f32[]")},
		{"f64[] as an ordinary main-level local", 0.02, mainLocalIs("f64[]")},
		{"(f32, f64) as an ordinary main-level local", 0.02, mainLocalIs("(f32, f64)")},
		{"Vec2 as an ordinary main-level local", 0.02, mainLocalIs("Vec2")},
		{"FShape as an ordinary main-level local", 0.02, mainLocalIs("FShape")},
		{"f32 as an ordinary main-level local", 0.02, mainLocalIs("f32")},
		{"f64 as an ordinary main-level local", 0.02, mainLocalIs("f64")},
		{"dynamic struct with a float field", 0.25, func(src, _ string) bool {
			return reDynStructFloatField.MatchString(src)
		}},
		{"dynamic enum variant with a float payload slot", 0.15, func(src, _ string) bool {
			return reDynEnumFloatPayload.MatchString(src)
		}},
		{"every slot of one float aggregate observed", 1.0, func(_, body string) bool {
			_, ok := floatAggObservation(body)
			return ok
		}},
	}
	hits := make([]int, len(landmarks))
	for seed := uint64(0); seed < seeds; seed++ {
		src := fernsmith.GenPrintableMain(seed)
		body := mainBodyOf(src)
		for i, l := range landmarks {
			if l.hit(src, body) {
				hits[i]++
			}
		}
	}
	for i, l := range landmarks {
		got := float64(hits[i]) / float64(seeds)
		if got < l.floor {
			t.Errorf("%s: %d/%d seeds (%.1f%%), want at least %.1f%% — the class is reachable in theory and unreached in practice",
				l.name, hits[i], seeds, 100*got, 100*l.floor)
		}
	}
}

func mainLocalIs(typeName string) func(src, body string) bool {
	return func(_, body string) bool {
		for _, m := range reMainLocalDecl.FindAllStringSubmatch(body, -1) {
			if strings.TrimSpace(m[1]) == typeName {
				return true
			}
		}
		return false
	}
}

func TestGenMainStaysFloatFree(t *testing.T) {
	banned := regexp.MustCompile(`\b(?:f32|f64|Vec2|FShape)\b`)
	for seed := uint64(0); seed < sweepN(t, 512); seed++ {
		src := fernsmith.GenMain(seed)
		if m := banned.FindString(src); m != "" {
			t.Fatalf("seed=%d: ProfileRunnable program mentions %q — floats must not reach the return-byte oracle\nsrc:\n%s", seed, m, src)
		}
	}
}

func TestGenBytesShrinkIsMonotonicAndValid(t *testing.T) {
	gens := []struct {
		name string
		gen  func([]byte) string
	}{
		{"GenBytes", fernsmith.GenBytes},
		{"GenMainBytes", fernsmith.GenMainBytes},
		{"GenPrintableMainBytes", fernsmith.GenPrintableMainBytes},
	}
	seeds := uint64(2)
	if os.Getenv("RUN_SHRINK_PROPERTY") == "1" {
		seeds = sweepN(t, 24)
	}
	shard, nshard, err := parseShard(os.Getenv("FERNSMITH_SHRINK_SHARD"))
	if err != nil {
		t.Fatalf("FERNSMITH_SHRINK_SHARD: %v", err)
	}
	for _, g := range gens {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			for seed := uint64(0); seed < seeds; seed++ {
				if seed%nshard != shard {
					continue
				}
				t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
					t.Parallel()
					r := rand.New(rand.NewPCG(seed, 0x5eed))
					corpus := randBytes(r, 192)

					prevSrc := g.gen(corpus)
					prevNodes, err := astNodeCount(t, prevSrc)
					if err != nil {
						t.Fatalf("full corpus does not type-check:\n%s\nerr: %v", prevSrc, err)
					}
					for n := len(corpus) - 1; n >= 0; n-- {
						src := g.gen(corpus[:n])
						nodes, err := astNodeCount(t, src)
						if err != nil {
							t.Fatalf("truncating to %d bytes produced a program that does not type-check — the shrinker would hand back a non-compiling repro\nsrc:\n%s\nerr: %v",
								n, src, err)
						}
						if nodes > prevNodes {
							t.Fatalf("truncating to %d bytes GREW the program (%d AST nodes > %d) — the shrinker can walk uphill from here\nsmaller corpus produced:\n%s\nlarger corpus produced:\n%s",
								n, nodes, prevNodes, src, prevSrc)
						}
						prevSrc, prevNodes = src, nodes
					}
				})
			}
		})
	}
}

func parseShard(raw string) (shard, nshard uint64, err error) {
	if raw == "" {
		return 0, 1, nil
	}
	i, n, ok := strings.Cut(raw, "/")
	if !ok {
		return 0, 0, fmt.Errorf("%q: want I/N, e.g. 0/2", raw)
	}
	if shard, err = strconv.ParseUint(i, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%q: %v", raw, err)
	}
	if nshard, err = strconv.ParseUint(n, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%q: %v", raw, err)
	}
	if nshard == 0 || shard >= nshard {
		return 0, 0, fmt.Errorf("%q: want 0 <= I < N", raw)
	}
	return shard, nshard, nil
}

func TestParseShard(t *testing.T) {
	for _, c := range []struct {
		raw           string
		shard, nshard uint64
		bad           bool
	}{
		{raw: "", shard: 0, nshard: 1},
		{raw: "0/2", shard: 0, nshard: 2},
		{raw: "1/2", shard: 1, nshard: 2},
		{raw: "2/2", bad: true},
		{raw: "0/0", bad: true},
		{raw: "1", bad: true},
		{raw: "a/2", bad: true},
		{raw: "0/-1", bad: true},
	} {
		shard, nshard, err := parseShard(c.raw)
		if c.bad {
			if err == nil {
				t.Errorf("parseShard(%q) = %d/%d, want an error", c.raw, shard, nshard)
			}
			continue
		}
		if err != nil || shard != c.shard || nshard != c.nshard {
			t.Errorf("parseShard(%q) = %d/%d, %v; want %d/%d", c.raw, shard, nshard, err, c.shard, c.nshard)
		}
	}
}

func astNodeCount(t *testing.T, src string) (int, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return 0, err
	}
	n := 0
	ast.WalkProgram(prog, func(ast.Node) bool { n++; return true })
	if err := constfold.Fold(prog, nil); err != nil {
		return 0, err
	}
	if _, err := checker.Check(prog); err != nil {
		return 0, err
	}
	return n, nil
}

func TestGenDeferBodyHasNoTry(t *testing.T) {
	src := fernsmith.GenMain(321)
	if err := checkGenerated(t, src); err != nil {
		t.Fatalf("GenMain(321) failed to type-check:\nsrc:\n%s\nerr: %v", src, err)
	}
	defers := 0
	for seed := uint64(0); seed < sweepN(t, 512); seed++ {
		src := fernsmith.GenMain(seed)
		for at := strings.Index(src, "defer { "); at >= 0; {
			depth := 0
			end := at
			for end < len(src) {
				if src[end] == '{' {
					depth++
				} else if src[end] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				end++
			}
			defers++
			if strings.Contains(src[at:end], "?)") {
				t.Fatalf("seed=%d: `?` inside a defer body:\n%s", seed, src[at:end+1])
			}
			next := strings.Index(src[end:], "defer { ")
			if next < 0 {
				break
			}
			at = end + next
		}
	}
	if defers == 0 {
		t.Fatal("no seed generated a defer, so the sweep checked nothing")
	}
}

func TestMainBytesReductionsAreValid(t *testing.T) {
	checked := 0
	for seed := uint64(0); seed < sweepN(t, 8); seed++ {
		r := rand.New(rand.NewPCG(seed, 0x5bad))
		corpus := randBytes(r, 2048)
		dels := fernsmith.MainBytesReductions(corpus)
		for i := 0; i < len(dels); i += len(dels)/8 + 1 {
			checked++
			src := fernsmith.GenMainBytes(dels[i])
			if _, err := astNodeCount(t, src); err != nil {
				t.Fatalf("seed %d move %d does not type-check:\n%s\nerr: %v", seed, i, src, err)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no corpus offered a move")
	}
}

func TestShrinkMainBytesReachesPastTruncation(t *testing.T) {
	mainOf := func(src string) string { return src[strings.Index(src, "\nfunction main"):] }
	fails := func(data []byte) bool { return strings.Contains(mainOf(fernsmith.GenMainBytes(data)), "while (") }
	truncate := func(data []byte) []byte {
		n := len(data)
		for step := n / 2; step >= 1; {
			if step < n && fails(data[:n-step]) {
				n -= step
				continue
			}
			step /= 2
		}
		return data[:n]
	}
	shrunk, beat := 0, 0
	for seed := uint64(0); seed < 64 && shrunk < 4; seed++ {
		corpus := randBytes(rand.New(rand.NewPCG(seed, 0x5bad)), 8192)
		if !fails(corpus) {
			continue
		}
		shrunk++
		trunc := fernsmith.GenMainBytes(truncate(corpus))
		data := fernsmith.ShrinkMainBytes(corpus, fails)
		src := fernsmith.GenMainBytes(data)
		if !fails(data) {
			t.Fatalf("seed %d: the shrunk corpus no longer fails:\n%s", seed, src)
		}
		if _, err := astNodeCount(t, src); err != nil {
			t.Fatalf("seed %d: the shrunk program does not type-check:\n%s\nerr: %v", seed, src, err)
		}
		if strings.Contains(src, "\nfunction gen_f") {
			t.Errorf("seed %d: a helper survived the shrink:\n%s", seed, src)
		}
		if len(src) > len(trunc) {
			t.Errorf("seed %d: shrunk program %d bytes, truncation alone %d", seed, len(src), len(trunc))
		}
		if len(src) < len(trunc) {
			beat++
		}
		t.Logf("seed %d: program %d bytes; truncation %d; shrink %d", seed, len(fernsmith.GenMainBytes(corpus)), len(trunc), len(src))
	}
	if shrunk == 0 {
		t.Fatal("no corpus in 64 put a while loop in main")
	}
	if beat == 0 {
		t.Errorf("deleting elements beat truncation on none of %d corpora", shrunk)
	}
}

func TestMainBytesReplaysTheSeed(t *testing.T) {
	for seed := uint64(0); seed < sweepN(t, 2048); seed++ {
		data, err := fernsmith.MainBytes(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if got, want := fernsmith.GenMainBytes(data), fernsmith.GenMain(seed); got != want {
			t.Fatalf("seed %d: the recorded corpus generates another program:\n%s\nwant:\n%s", seed, got, want)
		}
	}
	// A seed's corpus shrinks like any other: 122230 keeps its generic enum.
	data, err := fernsmith.MainBytes(122230)
	if err != nil {
		t.Fatal(err)
	}
	fails := func(d []byte) bool { return strings.Contains(fernsmith.GenMainBytes(d), "enum GO0") }
	shrunk := fernsmith.ShrinkMainBytes(data, fails)
	if src := fernsmith.GenMainBytes(shrunk); !fails(shrunk) || len(src) >= len(fernsmith.GenMain(122230)) {
		t.Errorf("seed 122230 did not shrink to a smaller program that keeps its generic enum:\n%s", src)
	}
}

func TestShrinkSourceDeletesLinesAndStatements(t *testing.T) {
	src := "struct A { x: i32 }\nstruct B { y: i32 }\nfunction f(): i32 { return 1; }\n" +
		"function main(): i32 { let a: i32 = 1; let b: i32 = f(); let c: i32 = 3; return b; }"
	keeps := []string{"let b: i32 = f()", "return 1", "return b"}
	got := fernsmith.ShrinkSource(src, func(s string) bool {
		for _, k := range keeps {
			if !strings.Contains(s, k) {
				return false
			}
		}
		return true
	})
	if want := "function f(): i32 { return 1; }\nfunction main(): i32 { let b: i32 = f(); return b; }"; got != want {
		t.Errorf("ShrinkSource =\n%s\nwant\n%s", got, want)
	}
}

func TestGenReachesOwnershipShapes(t *testing.T) {
	const seeds = 1024
	landmarks := []struct {
		name  string
		floor float64
		re    *regexp.Regexp
	}{
		{"struct field holding a box (33%)", 0.15, regexp.MustCompile(`struct S\d \{[^}]*: (i32\[\]|Option|Result|\(|Pair|Xyz|Map|S\d|E\d)`)},
		{"local of a program-declared array type (9%)", 0.04, regexp.MustCompile(`: [SE]\d\[\] =`)},
		{"projection through a nested value (10%)", 0.04, regexp.MustCompile(`[a-z_][a-z0-9_]*(\.[a-z0-9]+|\[0i32\])(\.[a-z0-9]+|\[0i32\])`)},
		{"functional update (18%)", 0.08, regexp.MustCompile(`\{ \.\.\.`)},
		{"ownership cluster (17%)", 0.08, regexp.MustCompile(`let __own_a\d+: `)},
		{"shared value reassigned (8%)", 0.03, regexp.MustCompile(`; __own_a\d+ = `)},
		{"fuel-bounded recursive helper (20%)", 0.10, regexp.MustCompile(`__fuel: i32`)},
		{"recursive self-call (14%)", 0.06, regexp.MustCompile(`\(__fuel - 1i32\)`)},
		{"state fold in main's return (57%)", 0.30, regexp.MustCompile(`\* 31i32 \+`)},
		{"closure cluster: parameter and capture of one type (23%)", 0.10, regexp.MustCompile(`let __clo_r\d+: i32 = `)},
		{"user trait with an impl (38%)", 0.18, regexp.MustCompile(`impl Tr0 for `)},
		{"two implementers of the trait (13%)", 0.06, regexp.MustCompile(`impl Tr0 for [^\n]*\nimpl Tr0 for `)},
		{"impl overriding the default method (20%)", 0.10, regexp.MustCompile(`; \} function t0_d\(self: Self\)`)},
		{"bounded generic call (22%)", 0.10, regexp.MustCompile(`tg0\(`)},
		{"call through a dyn parameter (21%)", 0.10, regexp.MustCompile(`(\(|, |\|> |= |return )dg0\(`)},
		{"trait cluster: a dyn array and a reassigned dyn local (15%)", 0.07, regexp.MustCompile(`let __dyn_a\d+: dyn Tr0\[\] = `)},
		{"associated function through a type parameter (9%)", 0.04, regexp.MustCompile(`(\(|, |\|> |= |return )tm0\(`)},
		{"generic trait, static and dyn (11%)", 0.05, regexp.MustCompile(`(\(|, |\|> |= |return )(tcg0|dc0)\(`)},
		{"projection of an associated type through a bound (6%)", 0.025, regexp.MustCompile(`(\(|, |\|> |= |return )tp0\(`)},
		{"dyn with a pinned associated type (3%)", 0.012, regexp.MustCompile(`(\(|, |\|> |= |return )dp0\(`)},
		{"own-receiver cluster (13%)", 0.06, regexp.MustCompile(`let __orv_c\d+: OB0 = __orv_b\d+\.push\(`)},
		{"own receiver past an alias of its array (8%)", 0.03, regexp.MustCompile(`let __orv_a\d+: i32\[\] = __orv_b\d+\.xs; `)},
		{"trait object in a struct field (9%)", 0.04, regexp.MustCompile(`: DH0 = DH0 \{ d: `)},
		{"trait object in a tuple (9%)", 0.04, regexp.MustCompile(`: \(dyn Tr0, i32\) = `)},
		{"as? downcast of a trait object (5%)", 0.02, regexp.MustCompile(` as\? S\d+\) \{ Some\(`)},
		{"generic struct and enum cluster (12%)", 0.06, regexp.MustCompile(`let __gen_a\d+: G0\[`)},
		{"generic instance over a boxed type (9%)", 0.04, regexp.MustCompile(`let __gen_a\d+: G0\[(string|i32\[\]|i64\[\]|boolean\[\]|Pair|Xyz|Map|Option|Result|\(|S\d|E\d)`)},
	}
	hits := make([]int, len(landmarks))
	for seed := uint64(0); seed < seeds; seed++ {
		src := fernsmith.GenMain(seed)
		for i, l := range landmarks {
			if l.re.MatchString(src) {
				hits[i]++
			}
		}
	}
	for i, l := range landmarks {
		if got := float64(hits[i]) / seeds; got < l.floor {
			t.Errorf("%s: %d/%d seeds (%.1f%%), want at least %.1f%%", l.name, hits[i], seeds, 100*got, 100*l.floor)
		}
	}
}
