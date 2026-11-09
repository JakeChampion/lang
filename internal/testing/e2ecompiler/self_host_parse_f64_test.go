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

const parseF64Driver = `import "std/io";
import "./util";
function main(): i32 {
  let src: string = "";
  match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
  let lines: string[] = src.split("\n");
  let i: i32 = 0;
  while (i < lines.len()) {
    if (lines[i].len() > 0) { print(util.parse_f64_bits(lines[i]).to_string()); }
    i = i + 1;
  }
  return 0;
}
`

func TestSelfHostParseF64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	runParseF64Driver(t, gcc, runner, copySelfHostTree(t), "pf64", parseF64Driver)
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
