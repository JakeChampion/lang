package e2e

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

// TestFernsmithMutationScore measures how much of the compiler fernsmith can
// actually check: it plants one small fault at a time in the lowering and rc
// sources, builds the faulty compiler, and asks whether any generated program
// tells it apart from the interpreter. The share of faults caught is the
// mutation score (Görz et al., USENIX Security 2023), the measure of a fuzzer
// that does not depend on which bugs happen to exist today; every fault it
// misses is a shape the generator does not reach or an oracle does not see,
// and the survivors it prints are that list.
//
// FERN_MUTATION_SCORE=N runs N mutants (FERN_MUTATION_BASE offsets the first
// id, so shards and nights draw different ones); FERN_MUTATION_SEEDS bounds
// the programs each mutant is tried on (default 200). Mutants are numbered,
// so `FERN_MUTATION_BASE=k FERN_MUTATION_SCORE=1` rebuilds mutant k.
//
// Each mutant is one of:
//   - a stillborn: the faulty sources do not build a compiler. Not scored.
//   - killed by smoke: it cannot compile a trivial program. Counted as
//     killed, but reported apart, since any test at all would catch it.
//   - killed: a fernsmith program exits differently, crashes, hangs, fails
//     to build where the unplanted compiler builds it, or trips the
//     sanitizer — every program is built under -sanitize, so a dropped
//     release is caught by the leak census and not only by a wrong answer.
//   - survived: none of the programs tell it apart. Some survivors are
//     equivalent mutants (the fault changes nothing), which no test can kill;
//     the rest are the generator's blind spots.
func TestFernsmithMutationScore(t *testing.T) {
	n := envInt(t, "FERN_MUTATION_SCORE", 0)
	if n == 0 {
		t.Skip("set FERN_MUTATION_SCORE=N to plant and score N compiler mutants")
	}
	base := envInt(t, "FERN_MUTATION_BASE", 0)
	seeds := envInt(t, "FERN_MUTATION_SEEDS", 200)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	good := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	sites := mutationSites(t, dir)
	if len(sites) == 0 {
		t.Fatal("no mutation sites in the staged compiler")
	}

	// The programs a mutant is judged on: those the unplanted compiler gets
	// right, with the interpreter's exit byte for each.
	var progs []string
	var wants []int
	sanitize := []string{"-sanitize"}
	quiet := func(string, ...any) {}
	for seed := uint64(0); len(progs) < seeds && seed < uint64(4*seeds); seed++ {
		src := fernsmith.GenMain(seed)
		want, err := interpRunExit(t, src)
		if err != nil {
			continue
		}
		r, gap := runSelfHostSeed(t, good, stdlibRoot, src, sanitize, nil, quiet)
		if gap != "" || r == nil || !r.exited || r.exit != want || strings.Contains(r.stderr, "fern-sanitizer:") {
			continue
		}
		progs, wants = append(progs, src), append(wants, want)
	}
	t.Logf("%d mutation sites; judging each mutant on %d programs", len(sites), len(progs))

	var stillborn, smoke, killed, survived int
	var report []string
	summary := func() string {
		s := fmt.Sprintf("mutation score: %d of %d non-trivial mutants killed by fernsmith (%d more killed by a trivial program, %d stillborn)",
			killed, killed+survived, smoke, stillborn)
		if killed+survived > 0 {
			s += fmt.Sprintf(" — %.0f%%", 100*float64(killed)/float64(killed+survived))
		}
		if len(report) < n {
			s += fmt.Sprintf(" — partial, %d of %d mutants", len(report), n)
		}
		return s + "\n" + strings.Join(report, "\n")
	}
	// The report is rewritten after every mutant, so a run the go test
	// timeout cuts short still leaves the mutants it finished.
	flush := func() {
		if path := os.Getenv("FERN_MUTATION_REPORT"); path != "" {
			if err := os.WriteFile(path, []byte(summary()+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
	}
	for id := base; id < base+n; id++ {
		m := pickMutation(sites, id)
		mdir := writeSelfHostAsmProject(t)
		copySelfHostDriver(t, mdir, "fern.fern")
		if err := m.apply(mdir); err != nil {
			t.Fatalf("mutant %d: %v", id, err)
		}
		bin := filepath.Join(mdir, "fern")
		if err := e2eharness.CompileWithSelfHost(t, e2eharness.Stage0Compiler(t), e2eharness.TargetX86_64Linux,
			filepath.Join(mdir, "fern.fern"), bin, e2eharness.DriverBuildWeightMB("fern.fern")); err != nil {
			stillborn++
			report = append(report, fmt.Sprintf("mutant %d stillborn  %s", id, m))
			flush()
			continue
		}
		verdict := mutantVerdict(t, bin, stdlibRoot, progs, wants)
		switch {
		case verdict == "smoke":
			smoke++
		case strings.HasPrefix(verdict, "killed"):
			killed++
		default:
			survived++
		}
		report = append(report, fmt.Sprintf("mutant %d %-9s %s", id, verdict, m))
		flush()
	}
	t.Log(summary())
}

// mutantVerdict is "smoke" when the mutant cannot build and run a trivial
// program, "killed@N (...)" when program N tells it apart, and "survived".
func mutantVerdict(t *testing.T, bin, stdlibRoot string, progs []string, wants []int) string {
	t.Helper()
	sanitize := []string{"-sanitize"}
	quiet := func(string, ...any) {}
	r, gap := runSelfHostSeed(t, bin, stdlibRoot, "function main(): i32 { return 7; }\n", sanitize, nil, quiet)
	if gap != "" || r == nil || !r.exited || r.exit != 7 {
		return "smoke"
	}
	for i, src := range progs {
		failed := ""
		note := func(format string, args ...any) { failed = fmt.Sprintf(format, args...) }
		r, gap := runSelfHostSeed(t, bin, stdlibRoot, src, sanitize, nil, note)
		switch {
		case r == nil && strings.Contains(failed, "did not finish"):
			return fmt.Sprintf("killed@%d (compiler hang)", i)
		case gap != "" || r == nil:
			return fmt.Sprintf("killed@%d (no binary)", i)
		case r.timedOut:
			return fmt.Sprintf("killed@%d (hang)", i)
		case !r.exited:
			return fmt.Sprintf("killed@%d (crash)", i)
		case strings.Contains(r.stderr, "fern-sanitizer:"):
			return fmt.Sprintf("killed@%d (sanitizer)", i)
		case r.exit != wants[i]:
			return fmt.Sprintf("killed@%d (exit %d, want %d)", i, r.exit, wants[i])
		}
	}
	return "survived"
}

// mutationFiles are the compiler sources faults are planted in: the
// lowering, the rc passes and the SSA backend this harness runs. The other
// targets' emitters are left out, since a fault there cannot reach an x86-64
// binary and would only add equivalent mutants, and so is asm_ir.fern, which
// is mostly the runtime library's emission and would outweigh the rest.
var mutationFiles = regexp.MustCompile(`^(ssa[a-z_]*|sem[a-z]*|rc[a-z]*|irverifyrc|lift|ir|ircore|constfold|flatten|x86_native|treeshake)\.fern$`)

// mutationOps are the operator swaps a site may take. Each is written with
// the spaces the compiler's own formatting puts around it, so a match is an
// operator rather than part of an identifier or a type.
var mutationOps = [][2]string{
	{" < ", " <= "}, {" <= ", " < "}, {" > ", " >= "}, {" >= ", " > "},
	{" == ", " != "}, {" != ", " == "}, {" && ", " || "}, {" || ", " && "},
	{" + 1", " + 2"}, {" - 1", " - 2"}, {" + ", " - "}, {" - ", " + "},
}

// mutationSite is one place a mutant can be planted.
type mutationSite struct {
	file string // relative to the staged dir
	line int    // 0-based
	col  int    // byte offset of the match in the line, -1 for a deletion
	from string
	to   string
}

func (m mutationSite) String() string {
	if m.col < 0 {
		return fmt.Sprintf("%s:%d delete %q", m.file, m.line+1, strings.TrimSpace(m.from))
	}
	return fmt.Sprintf("%s:%d %q -> %q", m.file, m.line+1, m.from, m.to)
}

// apply plants the mutant in dir.
func (m mutationSite) apply(dir string) error {
	path := filepath.Join(dir, m.file)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	ln := lines[m.line]
	if m.col < 0 {
		lines[m.line] = ""
	} else {
		if !strings.HasPrefix(ln[m.col:], m.from) {
			return fmt.Errorf("site %s no longer matches", m)
		}
		lines[m.line] = ln[:m.col] + m.to + ln[m.col+len(m.from):]
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// reAssignStmt is a reassignment statement on a line of its own — the
// deletion operator's candidates. A `let` is never deleted, since the
// name it binds is used later and the mutant would only be stillborn.
var reAssignStmt = regexp.MustCompile(`^\s+[a-z_][a-z0-9_.]* = [^=].*;\s*$`)

// mutationSites lists every site in dir's mutation files, in a fixed order.
func mutationSites(t *testing.T, dir string) []mutationSite {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if mutationFiles.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	var out []mutationSite
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(raw), "\n") {
			code := maskStrings(ln)
			if strings.HasPrefix(strings.TrimSpace(code), "//") {
				continue
			}
			if at := strings.Index(code, "//"); at >= 0 {
				code = code[:at]
			}
			for _, op := range mutationOps {
				// `+` also concatenates strings, and swapping that one only
				// builds a stillborn mutant.
				if strings.Contains(ln, `"`) && (op[0] == " + " || op[0] == " - ") {
					continue
				}
				for off := 0; ; {
					k := strings.Index(code[off:], op[0])
					if k < 0 {
						break
					}
					out = append(out, mutationSite{file: f, line: i, col: off + k, from: op[0], to: op[1]})
					off += k + len(op[0])
				}
			}
			if reAssignStmt.MatchString(code) {
				out = append(out, mutationSite{file: f, line: i, col: -1, from: ln})
			}
		}
	}
	return out
}

// maskStrings blanks the contents of string literals in a line, so an
// operator inside generated source text is not taken for one in the code.
func maskStrings(ln string) string {
	b := []byte(ln)
	in := false
	for i := 0; i < len(b); i++ {
		switch {
		case in && b[i] == '\\':
			b[i] = ' '
			if i+1 < len(b) {
				b[i+1] = ' '
				i++
			}
		case b[i] == '"':
			in = !in
		case in:
			b[i] = ' '
		}
	}
	return string(b)
}

// pickMutation is mutant id's site: a fixed draw, so the same id names the
// same fault on every run over the same sources.
func pickMutation(sites []mutationSite, id int) mutationSite {
	r := rand.New(rand.NewPCG(uint64(id), 0x6d757461))
	return sites[r.IntN(len(sites))]
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}
