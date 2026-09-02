package e2e

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/fernsmith"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Coverage-guided fuzzing of the self-host compiler (#5548 slice 4) ---
//
// Every other fuzz target in this repo drives the GO implementation, where
// `go test -fuzz` supplies the coverage feedback that makes a fuzzer a
// fuzzer rather than a random walk. Nothing supplied that for the SELF-HOST
// compiler, because Go's instrumentation cannot see inside a Fern binary —
// `FuzzSelfHostFrontEnd` says so in its own comment, and names this issue as
// what it was waiting for.
//
// `-cover` (slices 1-3) is what closes it. The loop is AFL-shaped:
//
//	build the self-host compiler ONCE with -cover
//	repeat:
//	  mutate a corpus entry -> fernsmith turns the bytes into a program
//	  run the instrumented compiler over it, read the counters it dumps
//	  keep the entry when it reached a counter nothing had reached before
//
// Feedback comes from process EXIT, which is the right granularity here and
// needs no live accessor: one input is one compiler process, so the report
// the binary already writes at exit is exactly this input's coverage.
//
// # Cost
//
// An iteration is a process spawn, a front-end run, and ~10 MB of counter
// rows to parse — measured at ~120 ms, so a 20-minute budget is ~10k inputs.
// That is four orders of magnitude slower per input than the in-process Go
// targets, which is why this is a nightly lane and not a per-PR one: it buys
// coverage of a compiler no existing lane can see inside of at all.

const (
	// selfHostCoverFuzzEnv gates the lane. Its own switch rather than
	// FERN_SELFHOST_FUZZ: that target throws arbitrary TEXT at the front end
	// with Go's mutation and no feedback, this one steers GENERATED programs
	// by measured self-host coverage, and the two want different budgets.
	selfHostCoverFuzzEnv = "FERN_SELFHOST_COVER_FUZZ"
	// selfHostCoverFuzzTimeEnv is the wall-clock budget for the guided run
	// (Go duration syntax). The default is small enough to be a sane local
	// smoke test; the nightly lane sets minutes.
	selfHostCoverFuzzTimeEnv = "FERN_SELFHOST_COVER_FUZZ_TIME"
	// selfHostCoverFuzzCorpusEnv points at a directory of corpus entries to
	// seed from and write discoveries back to. Unset means start from the
	// built-in seeds and keep nothing — which is what makes the lane
	// compound across nights only when CI caches the directory.
	selfHostCoverFuzzCorpusEnv = "FERN_SELFHOST_COVER_FUZZ_CORPUS"
)

// coverBits is a set of counter ordinals.
//
// The ordinal, not the site text, is the identity: the counter table is baked
// into the binary at compile time, so the Nth coverage row of one run is the
// Nth of every run. That makes the hit set a bitset over row positions and
// keeps the inner loop free of string keys — at ~148k rows per iteration the
// difference is the whole per-iteration budget.
type coverBits struct {
	words []uint64
	rows  int
}

func (c *coverBits) set(i int) {
	w := i / 64
	for len(c.words) <= w {
		c.words = append(c.words, 0)
	}
	c.words[w] |= 1 << (uint(i) % 64)
}

// addAll folds other into c, reporting how many counters c had not already
// reached. Zero means the input taught the corpus nothing.
func (c *coverBits) addAll(other *coverBits) int {
	gained := 0
	for w, bits := range other.words {
		for len(c.words) <= w {
			c.words = append(c.words, 0)
		}
		newBits := bits &^ c.words[w]
		if newBits != 0 {
			gained += popcount(newBits)
			c.words[w] |= newBits
		}
	}
	return gained
}

func (c *coverBits) count() int {
	n := 0
	for _, w := range c.words {
		n += popcount(w)
	}
	return n
}

func popcount(x uint64) int {
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

// buildInstrumentedSelfHost compiles the self-host CLI driver with -cover and
// links it, returning the binary's path.
//
// Deliberately NOT routed through BuildSelfHostBin: that consults a build
// cache keyed on the SOURCES, and an instrumented build has identical sources
// to an ordinary one. Sharing the key would hand this lane an uninstrumented
// binary — which reports nothing, so every input would look like it reached
// no new coverage and the fuzzer would silently degrade to a random walk. The
// failure would be invisible, which is exactly the shape this whole feature
// exists to refuse.
func buildInstrumentedSelfHost(t *testing.T, gcc, dir string) string {
	t.Helper()
	entry := filepath.Join(dir, "fern.fern")
	prog, _, err := modload.Load(entry)
	if err != nil {
		t.Fatalf("modload self-host driver: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold self-host driver: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check self-host driver: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph self-host driver: %v", err)
	}
	prev := ast.CoverEnabled
	t.Cleanup(func() { ast.CoverEnabled = prev })
	ast.CoverEnabled = true
	asm, emitErr := x86_64.Emit(prog, info)
	ast.CoverEnabled = prev
	if emitErr != nil {
		t.Fatalf("emit instrumented self-host driver: %v", emitErr)
	}
	asmPath := filepath.Join(dir, "fern_cover.s")
	binPath := filepath.Join(dir, "fern_cover")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link instrumented self-host driver: %v\n%s", err, out)
	}
	// The asm is tens of MB and nothing reads it again.
	_ = os.Remove(asmPath)
	return binPath
}

// runInstrumented type-checks one program with the instrumented driver and
// returns the counters it reached.
//
// stderr is streamed rather than buffered: the report is ~10 MB per run, and
// holding it whole would cost more than the compile it is measuring.
func runInstrumented(bin, stdlibRoot, srcPath, src string) (*coverBits, error) {
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		return nil, fmt.Errorf("write src: %w", err)
	}
	cmd := exec.Command(bin, "-check", srcPath)
	cmd.Env = append(os.Environ(), "FERN_STDLIB_ROOT="+stdlibRoot)
	cmd.Stdout = nil
	pipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	bits := &coverBits{}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	row := 0
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte(ast.CoverLinePrefix)) && !bytes.HasPrefix(line, []byte(ast.CoverBranchPrefix)) {
			continue
		}
		// The count is the last space-separated field. Anything but a
		// literal "0" means this counter was reached.
		if sp := bytes.LastIndexByte(line, ' '); sp >= 0 && !bytes.Equal(line[sp+1:], []byte("0")) {
			bits.set(row)
		}
		row++
	}
	scanErr := sc.Err()
	// A diagnostic exit is a normal outcome — most generated programs are
	// rejected, and rejecting them is the code path being measured. Only a
	// failure to read the report matters here.
	_ = cmd.Wait()
	if scanErr != nil {
		return nil, fmt.Errorf("read coverage report: %w", scanErr)
	}
	bits.rows = row
	return bits, nil
}

// coverFuzzSeeds are the starting corpus: byte strings fernsmith turns into
// programs. Short and varied rather than meaningful — the generator, not the
// bytes, decides what the program looks like.
func coverFuzzSeeds() [][]byte {
	return [][]byte{
		{},
		{0x00},
		{0x01, 0x02, 0x03, 0x04},
		{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa},
		[]byte("fernsmith"),
		bytes.Repeat([]byte{0x5a}, 32),
	}
}

// mutate returns a byte-level variant of in. Plain AFL-style havoc: the
// generator downstream is what turns a byte change into a structural one, so
// nothing here needs to know Fern's grammar.
func mutate(rng *rand.Rand, in []byte) []byte {
	out := append([]byte(nil), in...)
	switch {
	case len(out) == 0 || rng.Intn(4) == 0:
		// Grow. An empty entry can only grow, which is what lets a run
		// seeded with `{}` reach anything at all.
		out = append(out, byte(rng.Intn(256)))
	case rng.Intn(3) == 0:
		// Flip a bit.
		i := rng.Intn(len(out))
		out[i] ^= 1 << uint(rng.Intn(8))
	case rng.Intn(2) == 0:
		// Replace a byte.
		out[rng.Intn(len(out))] = byte(rng.Intn(256))
	default:
		// Truncate.
		out = out[:rng.Intn(len(out))]
	}
	// Keep entries bounded: fernsmith's program size grows with its input,
	// and an unbounded corpus entry turns one iteration into a minute.
	const maxEntry = 512
	if len(out) > maxEntry {
		out = out[:maxEntry]
	}
	return out
}

// fuzzRun is one budgeted campaign's result.
type fuzzRun struct {
	iterations int
	covered    int
	corpus     [][]byte
}

// runCoverageCampaign mutates from a corpus for a fixed number of iterations.
//
// `guided` is the whole experiment: with it on, an input that reached a new
// counter joins the corpus and its descendants inherit the progress; with it
// off, the corpus never grows and every input is a fresh mutation of a seed.
// Both arms get the same iteration count and the same RNG stream, so the
// difference between their final coverage is the feedback and nothing else.
func runCoverageCampaign(t *testing.T, bin, stdlibRoot, srcPath string, seeds [][]byte, iterations int, guided bool, rngSeed int64) fuzzRun {
	t.Helper()
	rng := rand.New(rand.NewSource(rngSeed))
	corpus := append([][]byte(nil), seeds...)
	total := &coverBits{}
	// Seed the accumulated set from the starting corpus so both arms begin
	// from the same coverage and the comparison measures discovery, not the
	// seeds.
	for _, entry := range corpus {
		bits, err := runInstrumented(bin, stdlibRoot, srcPath, fernsmith.GenMainBytes(entry))
		if err != nil {
			t.Fatalf("seed run: %v", err)
		}
		total.addAll(bits)
	}
	done := 0
	for ; done < iterations; done++ {
		parent := corpus[rng.Intn(len(corpus))]
		input := mutate(rng, parent)
		bits, err := runInstrumented(bin, stdlibRoot, srcPath, fernsmith.GenMainBytes(input))
		if err != nil {
			t.Fatalf("iteration %d: %v", done, err)
		}
		gained := total.addAll(bits)
		if guided && gained > 0 {
			corpus = append(corpus, input)
		}
	}
	return fuzzRun{iterations: done, covered: total.count(), corpus: corpus}
}

// TestSelfHostCoverageGuidedFuzz is the lane itself: a budgeted guided
// campaign that grows a corpus and persists it.
//
// It asserts the MECHANISM, which is deterministic — an entry joins the
// corpus only by reaching a counter nothing had reached, and the run must
// actually observe coverage rather than silently measuring an uninstrumented
// binary. The guided-beats-unguided comparison is its own test below, where
// its stochastic nature can be handled honestly.
func TestSelfHostCoverageGuidedFuzz(t *testing.T) {
	if os.Getenv(selfHostCoverFuzzEnv) == "" {
		t.Skip("set " + selfHostCoverFuzzEnv + "=1 to run the coverage-guided self-host fuzzer")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	budget := 30 * time.Second
	if v := os.Getenv(selfHostCoverFuzzTimeEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", selfHostCoverFuzzTimeEnv, v, err)
		}
		budget = d
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildInstrumentedSelfHost(t, gcc, dir)
	work := t.TempDir()
	srcPath := filepath.Join(work, "main.fern")

	corpusDir := os.Getenv(selfHostCoverFuzzCorpusEnv)
	corpus := append(coverFuzzSeeds(), loadCoverCorpus(t, corpusDir)...)

	total := &coverBits{}
	rng := rand.New(rand.NewSource(1))
	var iterations, discoveries int
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		parent := corpus[rng.Intn(len(corpus))]
		input := mutate(rng, parent)
		bits, err := runInstrumented(bin, stdlibRoot, srcPath, fernsmith.GenMainBytes(input))
		if err != nil {
			t.Fatalf("iteration %d: %v", iterations, err)
		}
		iterations++
		if total.rows == 0 {
			total.rows = bits.rows
		} else if bits.rows != total.rows {
			// The table is baked at compile time, so the row count cannot
			// move between runs of one binary. If it does, the ordinal is
			// not the identity it is assumed to be and every bit recorded
			// so far is suspect.
			t.Fatalf("iteration %d reported %d counter rows, previous runs reported %d — "+
				"the counter table must be fixed for a given binary",
				iterations, bits.rows, total.rows)
		}
		if total.addAll(bits) > 0 {
			corpus = append(corpus, input)
			discoveries++
		}
	}

	if iterations == 0 {
		t.Fatalf("budget %s ran no iterations", budget)
	}
	// The binary must actually be instrumented. Without this the whole lane
	// degrades silently into a random walk that reports success.
	if total.rows == 0 {
		t.Fatal("the self-host driver emitted no coverage rows — the build is not instrumented, " +
			"so nothing here is coverage-guided")
	}
	if total.count() == 0 {
		t.Fatalf("%d iterations reached zero counters out of %d — the report is being read wrong",
			iterations, total.rows)
	}
	t.Logf("coverage-guided self-host fuzz: %d iterations in %s, %d corpus entries (+%d found), %d/%d counters (%.1f%%)",
		iterations, budget, len(corpus), discoveries, total.count(), total.rows,
		100*float64(total.count())/float64(total.rows))
	saveCoverCorpus(t, corpusDir, corpus)
}

// TestSelfHostCoverageFeedbackBeatsBlindMutation is this issue's stated
// acceptance criterion: "a fernsmith run with feedback demonstrably reaches
// edges a feedback-less run of the same budget does not".
//
// Both arms get the same iteration count, the same seeds and the same RNG
// stream; the only difference is whether a discovery joins the corpus. The
// assertion is that feedback reaches strictly more counters — a tie would
// mean the corpus growth is doing nothing, which is the thing worth catching.
//
// Iteration-budgeted rather than time-budgeted on purpose: a wall-clock
// budget makes the comparison depend on how loaded the runner is, so a slow
// machine could hand the two arms different numbers of inputs and the result
// would say nothing about feedback.
func TestSelfHostCoverageFeedbackBeatsBlindMutation(t *testing.T) {
	if os.Getenv(selfHostCoverFuzzEnv) == "" {
		t.Skip("set " + selfHostCoverFuzzEnv + "=1 to run the coverage-guided self-host fuzzer")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildInstrumentedSelfHost(t, gcc, dir)
	work := t.TempDir()
	srcPath := filepath.Join(work, "main.fern")

	const iterations = 300
	seeds := coverFuzzSeeds()
	guided := runCoverageCampaign(t, bin, stdlibRoot, srcPath, seeds, iterations, true, 7)
	blind := runCoverageCampaign(t, bin, stdlibRoot, srcPath, seeds, iterations, false, 7)

	t.Logf("guided: %d counters from %d corpus entries; blind: %d counters from %d",
		guided.covered, len(guided.corpus), blind.covered, len(blind.corpus))
	if guided.covered <= blind.covered {
		t.Errorf("feedback reached %d counters, blind mutation reached %d — "+
			"the corpus is not steering anything",
			guided.covered, blind.covered)
	}
}

// loadCoverCorpus reads persisted corpus entries. A missing or unset
// directory is not an error: the lane simply starts from its seeds, which is
// what a first night does.
func loadCoverCorpus(t *testing.T, dir string) [][]byte {
	t.Helper()
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	// Sorted so a run's starting corpus does not depend on directory order,
	// which would make the campaign irreproducible from its seed.
	sort.Strings(names)
	var out [][]byte
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	return out
}

// saveCoverCorpus writes the corpus back for the next run to pick up. Entries
// are named by content, so re-saving an unchanged corpus rewrites the same
// files and a night that found nothing costs nothing.
func saveCoverCorpus(t *testing.T, dir string, corpus [][]byte) {
	t.Helper()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("corpus dir %s: %v (discoveries not persisted)", dir, err)
		return
	}
	for _, entry := range corpus {
		name := fmt.Sprintf("%x", sha256Sum(entry))
		if err := os.WriteFile(filepath.Join(dir, name), entry, 0o644); err != nil {
			t.Logf("write corpus entry: %v", err)
			return
		}
	}
}

// sha256Sum is spelled out rather than imported at the call site so the
// naming scheme reads as one thing: content-addressed, so two nights that
// discover the same entry do not both store it.
func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
