package e2e

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

func TestFernsmithShrinksAPlantedMiscompile(t *testing.T) {
	mutant := os.Getenv("FERN_FERNSMITH_MUTANT")
	if mutant == "" {
		t.Skip("set FERN_FERNSMITH_MUTANT to a compiler built by scripts/fernsmith-mutant")
	}
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

	quiet := func(string, ...any) {}
	verdict := func(compiler, src string) string {
		r, gap := runSelfHostSeed(t, compiler, stdlibRoot, src, nil, nil, quiet)
		switch {
		case gap != "" || r == nil:
			return "no binary"
		case r.timedOut:
			return "hang"
		case !r.exited:
			return "crash: " + r.state
		}
		return "exit " + strconv.Itoa(r.exit)
	}
	diverges := func(data []byte) (bool, string, string) {
		src := fernsmith.GenMainBytes(data)
		want, err := interpRunExit(t, src)
		if err != nil {
			return false, "", ""
		}
		w := "exit " + strconv.Itoa(want)
		got := verdict(mutant, src)
		return got != w && got != "no binary", w, got
	}

	seeds := uint64(1024)
	if v := os.Getenv("FERNSMITH_MUTANT_SEEDS"); v != "" {
		if seeds, err = strconv.ParseUint(v, 10, 64); err != nil {
			t.Fatalf("FERNSMITH_MUTANT_SEEDS=%q: %v", v, err)
		}
	}
	var data []byte
	caught := uint64(0)
	for seed := uint64(0); seed < seeds && data == nil; seed++ {
		candidate := mutantCorpus(seed)
		if bad, _, _ := diverges(candidate); bad {
			data, caught = candidate, seed
		}
	}
	if data == nil {
		t.Fatalf("the generator did not catch the planted miscompile in %d corpora", seeds)
	}
	original := fernsmith.GenMainBytes(data)

	tries := 0
	data = fernsmith.ShrinkMainBytes(data, func(cand []byte) bool {
		tries++
		bad, _, _ := diverges(cand)
		return bad
	})
	repro := fernsmith.GenMainBytes(data)
	_, want, got := diverges(data)
	if v := verdict(good, repro); v != want {
		t.Fatalf("the unplanted compiler gets the repro wrong too (%s, interpreter %s), so it shows another bug:\n%s", v, want, repro)
	}
	t.Logf("caught at corpus %d; %d corpus bytes shrank to %d in %d tries; program %d bytes to %d; interpreter %s, mutant %s\n%s",
		caught, mutantCorpusBytes, len(data), tries, programBytes(original), programBytes(repro), want, got, repro)
	if programBytes(repro) > mutantReproBytes {
		t.Errorf("the repro is %d bytes past the preamble, over the %d a planted closure swap needs", programBytes(repro), mutantReproBytes)
	}
}

const mutantCorpusBytes = 8192

const mutantReproBytes = 1024

func mutantCorpus(seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 6073))
	out := make([]byte, mutantCorpusBytes)
	for i := range out {
		out[i] = byte(r.Uint32())
	}
	return out
}

func programBytes(src string) int {
	if at := strings.Index(src, "\nfunction gen_f"); at >= 0 {
		return len(src) - at - 1
	}
	if at := strings.Index(src, "\nfunction main"); at >= 0 {
		return len(src) - at - 1
	}
	return len(src)
}
