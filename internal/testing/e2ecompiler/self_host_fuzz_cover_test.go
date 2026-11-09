package e2ecompiler

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Go's fuzzer steers by the coverage of the Go test binary, and it cannot see
// into the Fern process that reads each input. The loop here steers by the
// coverage of that process instead: `fern -cover` builds the program of a
// reader, the binary writes its counters at exit, and the loop keeps each
// input that reached a counter that no earlier input reached. The counters
// cover the modules that the program imports: the stdlib for stdparse.fern,
// and the compiler and the stdlib for a driver. docs/COVERAGE.md has the
// report format.

const (
	readerCoverFuzzEnv       = "FERN_READER_COVER_FUZZ"
	readerCoverFuzzTimeEnv   = "FERN_READER_COVER_FUZZ_TIME"
	readerCoverFuzzCorpusEnv = "FERN_READER_COVER_FUZZ_CORPUS"
)

// readerCoverProgram builds the program of r with -cover, with the shipped
// CLI: stdparse.fern, or a driver of compiler/drivers.
func readerCoverProgram(tb testing.TB, r readerTarget) string {
	fuzzBuildCache(tb)
	if _, runner := e2eharness.X86_64Tooling(tb); len(runner) != 0 {
		tb.Skip("the fuzz programs run natively only")
	}
	b := e2eharness.CLIBuild{
		Target: e2eharness.TargetX86_64Linux,
		Dir:    e2eharness.RepoPath("tests", "fuzz"),
		Entry:  "stdparse.fern",
		Args:   []string{"-cover"},
	}
	if r.program != stdparse {
		b.Dir, b.Entry = e2eharness.RepoPath("compiler"), "drivers/"+r.program+".fern"
	}
	return e2eharness.CachedCLIBuild(tb, b)
}

// runCover runs the -cover build of stdparse on one input. The run it
// returns has the program's own stderr, without the report.
func runCover(tb testing.TB, bin, path string, r readerTarget, input []byte) (*e2eharness.CoverBits, fuzzRun) {
	tb.Helper()
	if err := os.WriteFile(path, input, 0o644); err != nil {
		tb.Fatal(err)
	}
	run := runUnderBudget(exec.Command(bin, r.reader, path))
	bits, rest, err := e2eharness.ReadCoverReport(strings.NewReader(run.stderr))
	if err != nil {
		tb.Fatal(err)
	}
	run.stderr = rest
	return bits, run
}

// boundaryBytes are the byte values at the edges of the signed and unsigned
// ranges, which length and count fields are most likely to mishandle.
var boundaryBytes = []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff}

// mutateHostile makes one to four random changes to a copy of in. A change
// adds, flips, sets, deletes or copies bytes, writes a boundary value, cuts
// the end, or puts in a block of another corpus entry.
func mutateHostile(rng *rand.Rand, in []byte, corpus [][]byte) []byte {
	out := slices.Clone(in)
	for range 1 + rng.Intn(4) {
		n := len(out)
		block := func(of []byte) []byte {
			i := rng.Intn(len(of))
			return of[i : i+1+rng.Intn(min(len(of)-i, 64))]
		}
		switch op := rng.Intn(8); {
		case n == 0 || op == 0:
			out = slices.Insert(out, rng.Intn(n+1), byte(rng.Intn(256)))
		case op == 1:
			out[rng.Intn(n)] ^= 1 << uint(rng.Intn(8))
		case op == 2:
			out[rng.Intn(n)] = byte(rng.Intn(256))
		case op == 3:
			out[rng.Intn(n)] = boundaryBytes[rng.Intn(len(boundaryBytes))]
		case op == 4:
			i := rng.Intn(n)
			out = slices.Delete(out, i, i+1+rng.Intn(min(n-i, 64)))
		case op == 5:
			out = slices.Insert(out, rng.Intn(n+1), slices.Clone(block(out))...)
		case op == 6:
			if donor := corpus[rng.Intn(len(corpus))]; len(donor) > 0 {
				out = slices.Insert(out, rng.Intn(n+1), block(donor)...)
			}
		default:
			out = out[:rng.Intn(n)]
		}
	}
	if len(out) > fuzzInputLimit {
		out = out[:fuzzInputLimit]
	}
	return out
}

// keepFailingInput writes input to the testdata corpus of target, in the
// format of Go's fuzzer, so that each run of the package replays it.
func keepFailingInput(tb testing.TB, target string, input []byte) {
	tb.Helper()
	dir := filepath.Join("testdata", "fuzz", target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	entry := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", input)
	path := filepath.Join(dir, fmt.Sprintf("%x", sha256.Sum256([]byte(entry)))[:16])
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		tb.Fatal(err)
	}
	tb.Logf("the failing input is in %s: commit it with the fix", path)
}

// coverCampaign is the state of one coverage-guided loop over one reader.
type coverCampaign struct {
	tb     testing.TB
	bin    string
	path   string
	r      readerTarget
	total  e2eharness.CoverBits
	corpus [][]byte
	runs   int
}

func newCoverCampaign(tb testing.TB, bin string, r readerTarget, seeds [][]byte) *coverCampaign {
	c := &coverCampaign{tb: tb, bin: bin, path: filepath.Join(tb.TempDir(), "input"), r: r}
	for _, seed := range seeds {
		if len(seed) <= fuzzInputLimit {
			c.try(seed, true)
		}
	}
	return c
}

// try runs one input and reports whether it reached a new counter. A fault
// stops the test after the input is kept. With keep, an input that reached a
// new counter joins the corpus.
func (c *coverCampaign) try(input []byte, keep bool) bool {
	c.tb.Helper()
	bits, run := runCover(c.tb, c.bin, c.path, c.r, input)
	c.runs++
	if fault := readerFault(c.r, input, run); fault != "" {
		keepFailingInput(c.tb, c.r.target, input)
		c.tb.Fatalf("%s\ninput: %x", fault, input)
	}
	if bits.Rows == 0 {
		c.tb.Fatalf("%s gave no coverage rows, so the build is not instrumented", c.r.reader)
	}
	if c.total.Rows == 0 {
		c.total.Rows = bits.Rows
	} else if bits.Rows != c.total.Rows {
		c.tb.Fatalf("a run gave %d coverage rows and an earlier run gave %d, but the table of one binary is fixed", bits.Rows, c.total.Rows)
	}
	gained := c.total.AddAll(bits) > 0
	if gained && keep {
		c.corpus = append(c.corpus, input)
	}
	return gained
}

// step mutates one corpus entry and runs it.
func (c *coverCampaign) step(rng *rand.Rand, keep bool) bool {
	pool := c.corpus
	if len(pool) == 0 {
		pool = [][]byte{{}}
	}
	return c.try(mutateHostile(rng, pool[rng.Intn(len(pool))], pool), keep)
}

// TestSelfHostReaderCoverageGuidedFuzz runs the coverage-guided loop on each
// reader of readerTargets, with the seeds and the check of its Go fuzz target.
// FERN_READER_COVER_FUZZ_TIME is the budget of each reader, and
// FERN_READER_COVER_FUZZ_CORPUS is a directory that keeps the corpus of each
// reader from one run to the next. Run one reader with
// -run '^TestSelfHostReaderCoverageGuidedFuzz$/^FuzzSelfHostStdJSON$'.
func TestSelfHostReaderCoverageGuidedFuzz(t *testing.T) {
	if !envflag.On(readerCoverFuzzEnv) {
		t.Skip("set " + readerCoverFuzzEnv + "=1 to run the coverage-guided reader fuzzer")
	}
	budget := 30 * time.Second
	if v := os.Getenv(readerCoverFuzzTimeEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", readerCoverFuzzTimeEnv, v, err)
		}
		budget = d
	}
	for _, r := range readerTargets {
		t.Run(r.target, func(t *testing.T) {
			bin := readerCoverProgram(t, r)
			dir := os.Getenv(readerCoverFuzzCorpusEnv)
			if dir != "" {
				dir = filepath.Join(dir, r.reader)
			}
			seeds := r.seeds(t)
			c := newCoverCampaign(t, bin, r, append(seeds, e2eharness.LoadCoverCorpus(dir)...))
			fromSeeds := c.total.Count()
			seed := time.Now().UnixNano()
			rng := rand.New(rand.NewSource(seed))
			found := 0
			for deadline := time.Now().Add(budget); time.Now().Before(deadline); {
				if c.step(rng, true) {
					found++
				}
			}
			t.Logf("%s: %d runs in %s (random seed %d), %d corpus entries (+%d found), %d of %d counters (the seeds and the saved corpus reached %d)",
				r.reader, c.runs, budget, seed, len(c.corpus), found, c.total.Count(), c.total.Rows, fromSeeds)
			e2eharness.SaveCoverCorpus(t, dir, c.corpus)
		})
	}
}

// TestSelfHostReaderCoverageBeatsBlind runs the loop twice from the same seeds
// and the same random streams. One arm keeps each input that reached a new
// counter, and the other keeps none. Summed over the readers and the streams,
// the arm with feedback must reach more counters, or the loop is not steering
// anything. The seeds are almost empty, because a reader's own seeds reach
// most of what a few hundred blind mutations near them can reach.
func TestSelfHostReaderCoverageBeatsBlind(t *testing.T) {
	if !envflag.On(readerCoverFuzzEnv) {
		t.Skip("set " + readerCoverFuzzEnv + "=1 to run the coverage-guided reader fuzzer")
	}
	const runs = 600
	const streams = 2
	readers := []string{"inflate", "url", "protobuf", "tangle", "index"}
	seeds := [][]byte{{}, []byte("0")}
	guided := make([]int, len(readers))
	blind := make([]int, len(readers))
	bins := make([]string, len(readers))
	for i, name := range readers {
		bins[i] = readerCoverProgram(t, readerTargetNamed(t, name))
	}
	t.Run("readers", func(t *testing.T) {
		for i, name := range readers {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				r := readerTargetNamed(t, name)
				arm := func(keep bool, stream int64) int {
					c := newCoverCampaign(t, bins[i], r, seeds)
					rng := rand.New(rand.NewSource(stream))
					for range runs {
						c.step(rng, keep)
					}
					return c.total.Count()
				}
				for stream := int64(1); stream <= streams; stream++ {
					guided[i] += arm(true, stream)
					blind[i] += arm(false, stream)
				}
				t.Logf("%s: feedback reached %d counters, blind mutation %d", name, guided[i], blind[i])
			})
		}
	})
	if t.Failed() {
		return
	}
	var g, b int
	for i := range readers {
		g += guided[i]
		b += blind[i]
	}
	if g <= b {
		t.Errorf("over %v and %d streams of %d runs, feedback reached %d counters and blind mutation %d (per reader: feedback %v, blind %v)",
			readers, streams, runs, g, b, guided, blind)
	}
}

func readerTargetNamed(tb testing.TB, reader string) readerTarget {
	for _, r := range readerTargets {
		if r.reader == reader {
			return r
		}
	}
	tb.Fatalf("no reader %s in readerTargets", reader)
	return readerTarget{}
}
