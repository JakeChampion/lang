package e2ecompiler

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func exactMidpoint(lo, hi float64) string {
	m := new(big.Rat).Add(new(big.Rat).SetFloat64(lo), new(big.Rat).SetFloat64(hi))
	m.Quo(m, big.NewRat(2, 1))
	s := strings.TrimRight(m.FloatString(1100), "0")
	return strings.TrimSuffix(s, ".")
}

func parseF64Corpus() (lits []string, want []int64) {
	add := func(s string) {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
				return
			}
		}
		lits = append(lits, s)
		want = append(want, int64(math.Float64bits(v)))
	}
	for _, s := range []string{
		"0", "1", "2.0", "0.5", "84.0", "0.1", "0.2", "0.3", "1e10", "-1e10",
		"1.4426950408889634", "0.6931471805599453", "1.5707963267948966",
		"1.4142135623730951", "0.00019841269841269841", "0.0013888888888888889",
		"0.0083333333333333332", "0.041666666666666664", "0.16666666666666666",
		"-0.00019841269841269841", "0.090909090909090912",
		"1.7976931348623157e308", "2.2250738585072014e-308", "5e-324", "4.9e-324",
		"2.4703282292062327e-324", "2.4703282292062328e-324",
		"1e309", "-1e309", "1e999", "1e-324", "1e-325", "1e-400",
		"9007199254740993", "9007199254740992", "123456789012345678901234567890",
		"0.000000000000000000000000000001", "3.141592653589793", "2.718281828459045",
		"1.7976931348623158e308", "1.797693134862315808e308", "2.5e-324", "2.4e-324",
	} {
		add(s)
	}
	for _, d := range []float64{
		1.0, 2.0, 1.5, 3.0, 1024.0, 0.1, 1e30, 1e100, 1e-100,
		math.Ldexp(1, -1022),
		math.Ldexp(1, -1074),
		math.Ldexp(3, -1074),
		math.Ldexp(1, -1073),
	} {
		add(exactMidpoint(d, math.Nextafter(d, math.Inf(1))))
	}
	add(exactMidpoint(0, math.Ldexp(1, -1074)))
	r := rand.New(rand.NewSource(5404))
	for i := 0; i < 300; i++ {
		f := math.Float64frombits(r.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		add(strconv.FormatFloat(f, 'g', -1, 64))
		add(strconv.FormatFloat(f, 'e', 17, 64))
	}
	for i := 0; i < 150; i++ {
		add(fmt.Sprintf("%d.%09d", r.Intn(1000), r.Intn(1e9)))
		add(fmt.Sprintf("%d.%018de%d", r.Intn(10), uint64(r.Int63())%1e18, r.Intn(60)-30))
	}
	return lits, want
}

func runParseF64Driver(t *testing.T, gcc string, runner []string, dir, name, progSrc string) {
	t.Helper()
	path := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(path, []byte(progSrc), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	bin := buildSelfHostBin(t, gcc, dir, name+".fern", name)
	lits, want := parseF64Corpus()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(strings.Join(lits, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver run: %v", err)
	}
	got := strings.Fields(string(bytes.TrimSpace(out)))
	if len(got) != len(lits) {
		t.Fatalf("driver printed %d results for %d literals", len(got), len(lits))
	}
	bad := 0
	for i := range lits {
		g, perr := strconv.ParseInt(got[i], 10, 64)
		if perr != nil || g != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("%q: got bits %s, want %d", lits[i], got[i], want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches", bad-10)
	}
}

const parseF64DriverBody = `
function main(): i32 {
  let src: string = "";
  match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
  let lines: string[] = src.split("\n");
  let i: i32 = 0;
  while (i < lines.len()) {
    if (lines[i].len() > 0) { print(f64_bits(PARSE(lines[i])).to_string()); }
    i = i + 1;
  }
  return 0;
}
`

func TestSelfHostParseF64Watbin(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	src, err := os.ReadFile("../../../compiler/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "watbin.fern"), src, 0o644); err != nil {
		t.Fatalf("write watbin: %v", err)
	}
	prog := "import \"std/io\";\nimport \"./watbin\";\n" +
		strings.ReplaceAll(parseF64DriverBody, "PARSE", "watbin.parse_f64")
	runParseF64Driver(t, gcc, runner, dir, "pf64_watbin", prog)
}

func TestSelfHostParseF64X86Gas(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	nat := mustRead(t, "../../../compiler/x86_native.fern")
	prog := "import \"std/io\";\n" + string(nat) + "\n" +
		strings.ReplaceAll(parseF64DriverBody, "PARSE", "x86_gas_parse_f64")
	runParseF64Driver(t, gcc, runner, dir, "pf64_x86gas", prog)
}

func TestSelfHostParseF64Arm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	a64 := mustRead(t, "../../../compiler/arm64_native.fern")
	prog := "import \"std/io\";\n" + string(a64) + "\n" +
		strings.ReplaceAll(parseF64DriverBody, "PARSE", "arm64_parse_f64")
	runParseF64Driver(t, gcc, runner, dir, "pf64_arm64", prog)
}

const f64RangeDriverBody = `
function main(): i32 {
  let src: string = "";
  match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
  let lines: string[] = src.split("\n");
  let i: i32 = 0;
  while (i < lines.len()) {
    if (lines[i].len() > 0) {
      if (f64_lit_out_of_range(lines[i]) ) { print("1"); } else { print("0"); }
    }
    i = i + 1;
  }
  return 0;
}
`

func TestSelfHostF64LitOutOfRangeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	util := mustRead(t, "../../../compiler/util.fern")
	prog := "import \"std/io\";\n" + string(util) + "\n" + f64RangeDriverBody
	path := filepath.Join(dir, "f64range.fern")
	if err := os.WriteFile(path, []byte(prog), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "f64range.fern", "f64range")

	lits, wantBits := parseF64Corpus()
	for e := 300; e <= 320; e++ {
		for _, m := range []string{"1", "1.5", "9.99", "0.5"} {
			lits = append(lits, fmt.Sprintf("%se%d", m, e))
			v, _ := strconv.ParseFloat(lits[len(lits)-1], 64)
			wantBits = append(wantBits, int64(math.Float64bits(v)))
		}
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(strings.Join(lits, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver run: %v", err)
	}
	got := strings.Fields(string(bytes.TrimSpace(out)))
	if len(got) != len(lits) {
		t.Fatalf("driver printed %d answers for %d literals", len(got), len(lits))
	}
	bad := 0
	for i := range lits {
		want := "0"
		if math.IsInf(math.Float64frombits(uint64(wantBits[i])), 0) {
			want = "1"
		}
		if got[i] != want {
			bad++
			if bad <= 10 {
				t.Errorf("%q: out-of-range = %s, want %s", lits[i], got[i], want)
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches", bad-10)
	}
}

var pf64Mirrors = []string{"util", "watbin", "x86_native", "arm64_native"}

var pf64MirrorFuncs = []string{"parse_f64_bits", "pf64_div2", "pf64_mul2", "pf64_sub", "pf64_all_zero"}

func pf64FuncBody(t *testing.T, module, name string) string {
	t.Helper()
	src := string(mustRead(t, "../../../compiler/"+module+".fern"))
	open := regexp.MustCompile(`^(pub )?function ` + name + `\(`)
	var kept []string
	started := false
	for _, line := range strings.Split(src, "\n") {
		if !started && open.MatchString(line) {
			started = true
		}
		if !started {
			continue
		}
		done := line == "}"
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, strings.TrimPrefix(line, "pub "))
		}
		if done {
			break
		}
	}
	if len(kept) == 0 {
		t.Fatalf("%s.fern: no %s definition", module, name)
	}
	return strings.Join(kept, "\n")
}

func TestSelfHostParseF64MirrorsAgree(t *testing.T) {
	for _, fn := range pf64MirrorFuncs {
		t.Run(fn, func(t *testing.T) {
			want := pf64FuncBody(t, pf64Mirrors[0], fn)
			for _, m := range pf64Mirrors[1:] {
				if got := pf64FuncBody(t, m, fn); got != want {
					t.Errorf("%s.fern's %s has drifted from %s.fern's:\n--- %s\n%s\n--- %s\n%s",
						m, fn, pf64Mirrors[0], m, got, pf64Mirrors[0], want)
				}
			}
		})
	}
}
