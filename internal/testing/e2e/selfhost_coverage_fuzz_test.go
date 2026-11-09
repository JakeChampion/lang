package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const (
	selfHostCoverFuzzEnv       = "FERN_SELFHOST_COVER_FUZZ"
	selfHostCoverFuzzTimeEnv   = "FERN_SELFHOST_COVER_FUZZ_TIME"
	selfHostCoverFuzzCorpusEnv = "FERN_SELFHOST_COVER_FUZZ_CORPUS"
)

func buildInstrumentedSelfHost(t *testing.T, dir string) string {
	t.Helper()
	binPath := filepath.Join(dir, "fern_cover")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-cover", "-target", e2eharness.TargetX86_64Linux, "-o", binPath, filepath.Join(dir, "fern.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build instrumented self-host driver: %v\n%s", err, out)
	}
	return binPath
}

func runInstrumented(bin, stdlibRoot, srcPath, src string) (*e2eharness.CoverBits, error) {
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
	bits, _, err := e2eharness.ReadCoverReport(pipe)
	_ = cmd.Wait()
	return bits, err
}

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

func mutate(rng *rand.Rand, in []byte) []byte {
	out := append([]byte(nil), in...)
	switch {
	case len(out) == 0 || rng.Intn(4) == 0:
		out = append(out, byte(rng.Intn(256)))
	case rng.Intn(3) == 0:
		i := rng.Intn(len(out))
		out[i] ^= 1 << uint(rng.Intn(8))
	case rng.Intn(2) == 0:
		out[rng.Intn(len(out))] = byte(rng.Intn(256))
	default:
		out = out[:rng.Intn(len(out))]
	}
	const maxEntry = 512
	if len(out) > maxEntry {
		out = out[:maxEntry]
	}
	return out
}

type fuzzRun struct {
	iterations int
	covered    int
	corpus     [][]byte
}

func runCoverageCampaign(t *testing.T, bin, stdlibRoot, srcPath string, seeds [][]byte, iterations int, guided bool, rngSeed int64) fuzzRun {
	t.Helper()
	rng := rand.New(rand.NewSource(rngSeed))
	corpus := append([][]byte(nil), seeds...)
	total := &e2eharness.CoverBits{}
	for _, entry := range corpus {
		bits, err := runInstrumented(bin, stdlibRoot, srcPath, fernsmith.GenMainBytes(entry))
		if err != nil {
			t.Fatalf("seed run: %v", err)
		}
		total.AddAll(bits)
	}
	done := 0
	for ; done < iterations; done++ {
		parent := corpus[rng.Intn(len(corpus))]
		input := mutate(rng, parent)
		bits, err := runInstrumented(bin, stdlibRoot, srcPath, fernsmith.GenMainBytes(input))
		if err != nil {
			t.Fatalf("iteration %d: %v", done, err)
		}
		gained := total.AddAll(bits)
		if guided && gained > 0 {
			corpus = append(corpus, input)
		}
	}
	return fuzzRun{iterations: done, covered: total.Count(), corpus: corpus}
}

func TestSelfHostCoverageGuidedFuzz(t *testing.T) {
	if !envflag.On(selfHostCoverFuzzEnv) {
		t.Skip("set " + selfHostCoverFuzzEnv + "=1 to run the coverage-guided self-host fuzzer")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
	bin := buildInstrumentedSelfHost(t, dir)
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	work := t.TempDir()
	srcPath := filepath.Join(work, "main.fern")

	corpusDir := os.Getenv(selfHostCoverFuzzCorpusEnv)
	corpus := append(coverFuzzSeeds(), e2eharness.LoadCoverCorpus(corpusDir)...)

	total := &e2eharness.CoverBits{}
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
		if total.Rows == 0 {
			total.Rows = bits.Rows
		} else if bits.Rows != total.Rows {
			t.Fatalf("iteration %d reported %d counter rows, previous runs reported %d — "+
				"the counter table must be fixed for a given binary",
				iterations, bits.Rows, total.Rows)
		}
		if total.AddAll(bits) > 0 {
			corpus = append(corpus, input)
			discoveries++
			judgeDiscovery(t, fernBin, stdlibRoot, discoveries, input)
		}
	}

	if iterations == 0 {
		t.Fatalf("budget %s ran no iterations", budget)
	}
	if total.Rows == 0 {
		t.Fatal("the self-host driver emitted no coverage rows — the build is not instrumented, " +
			"so nothing here is coverage-guided")
	}
	if total.Count() == 0 {
		t.Fatalf("%d iterations reached zero counters out of %d — the report is being read wrong",
			iterations, total.Rows)
	}
	t.Logf("coverage-guided self-host fuzz: %d iterations in %s, %d corpus entries (+%d found), %d/%d counters (%.1f%%)",
		iterations, budget, len(corpus), discoveries, total.Count(), total.Rows,
		100*float64(total.Count())/float64(total.Rows))
	e2eharness.SaveCoverCorpus(t, corpusDir, corpus)
}

func judgeDiscovery(t *testing.T, fernBin, stdlibRoot string, n int, input []byte) {
	t.Helper()
	t.Run(fmt.Sprintf("discovery=%d", n), func(t *testing.T) {
		src := fernsmith.GenMainBytes(input)
		want := runInterpByteOrSkip(t, src)
		failf := func(format string, args ...any) {
			t.Errorf("input %x: "+format, append([]any{input}, args...)...)
		}
		r, gap := runSelfHostSeed(t, fernBin, stdlibRoot, src, nil, nil, failf)
		if gap != "" {
			if compilerRefused(gap) {
				failf("the compiler refused this program:\n%s", gap)
				return
			}
			t.Skipf("self-host coverage gap: %s", gap)
		}
		if r != nil {
			checkSelfHostSeedExit(*r, want, src, failf)
		}
	})
}

func TestSelfHostCoverageFeedbackBeatsBlindMutation(t *testing.T) {
	if !envflag.On(selfHostCoverFuzzEnv) {
		t.Skip("set " + selfHostCoverFuzzEnv + "=1 to run the coverage-guided self-host fuzzer")
	}
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildInstrumentedSelfHost(t, dir)

	const iterations = 300
	const streams = 5
	seeds := coverFuzzSeeds()
	guided := make([]int, streams)
	blind := make([]int, streams)
	t.Run("streams", func(t *testing.T) {
		for i := 0; i < streams; i++ {
			t.Run(fmt.Sprintf("rng%d", i+1), func(t *testing.T) {
				t.Parallel()
				srcPath := filepath.Join(t.TempDir(), "main.fern")
				g := runCoverageCampaign(t, bin, stdlibRoot, srcPath, seeds, iterations, true, int64(i+1))
				b := runCoverageCampaign(t, bin, stdlibRoot, srcPath, seeds, iterations, false, int64(i+1))
				t.Logf("guided: %d counters from %d corpus entries; blind: %d counters from %d",
					g.covered, len(g.corpus), b.covered, len(b.corpus))
				guided[i], blind[i] = g.covered, b.covered
			})
		}
	})
	if t.Failed() {
		return
	}
	var g, b int
	for i := range guided {
		g += guided[i]
		b += blind[i]
	}
	if g <= b {
		t.Errorf("over %d streams feedback reached %d counters, blind mutation reached %d — "+
			"the corpus is not steering anything (per stream: guided %v, blind %v)",
			streams, g, b, guided, blind)
	}
}
