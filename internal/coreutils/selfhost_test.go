package coreutils

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host leg of the parity gate: every utility here compiled by the
// SELF-HOST compiler (`examples/self_host/fern.fern`) rather than the native
// one, run over the same corpus, and required to agree with the native build
// byte for byte.
//
// It exists because nothing else compiles this tree with the self-host
// compiler. The gate above uses the native binary, so `coreutils/lib/gnu.fern`
// spent its whole life outside the self-host's reach: its getopt cursor returns
// `(Option[OptMatch], Getopt)`, a tuple whose Option element carries a struct
// payload, and the self-host tuple lowering refused that shape — every utility
// declaring an option bailed the module (#8407). A gap like that is invisible
// until something compiles the tree both ways.
//
// The comparison is against the NATIVE binary, not against GNU: native is
// already held to GNU byte for byte by the corpus, so agreeing with it is
// agreeing with GNU, and a failure here says "the two compilers disagree"
// rather than re-reporting a parity bug in both.

// corpusByUtil maps each utility to its cases — the SAME functions the GNU
// parity gate calls, so this leg cannot quietly test something narrower.
// A utility missing from here fails TestSelfHostCoreutilsCoverage.
func corpusByUtil() map[string]func(*testing.T) []invocation {
	return map[string]func(*testing.T) []invocation{
		"[":        bracketCases,
		"basename": basenameCases,
		"comm":     commCases,
		"dirname":  dirnameCases,
		"echo":     echoCases,
		"expr":     exprCases,
		"factor":   factorCases,
		"false":    trueFalseCases,
		"head":     headCases,
		"hostid":   hostidCases,
		"numfmt":   numfmtCases,
		"printf":   printfCases,
		"seq":      seqCases,
		"sleep":    sleepCases,
		"test":     testCases,
		"true":     trueFalseCases,
		"tsort":    tsortCases,
		"uniq":     uniqCases,
		"wc":       wcCases,
		"yes":      yesCases,
	}
}

// utilNames lists the utilities in coreutils/, from the directory rather than
// from a list here: a new one joins this gate by existing.
func utilNames(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "coreutils", "*.fern"))
	if err != nil {
		t.Fatalf("glob coreutils: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no coreutils/*.fern found — the gate is looking in the wrong place")
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".fern"))
	}
	return names
}

// TestSelfHostCoreutilsCoverage fails when a utility has no self-host cases,
// so adding one cannot silently skip this leg.
func TestSelfHostCoreutilsCoverage(t *testing.T) {
	corpus := corpusByUtil()
	for _, util := range utilNames(t) {
		if _, ok := corpus[util]; !ok {
			t.Errorf("coreutils/%s.fern has no entry in corpusByUtil — every utility is compiled and run both ways, so add its cases function", util)
		}
	}
}

var (
	selfHostOnce sync.Once
	selfHostPath string
	selfHostFail string

	selfHostBinsMu sync.Mutex
	selfHostBins   = map[string]string{}
	selfHostBinDir string
)

// selfHostCompiler builds the self-host compiler for the host target, once per
// test process. It is the native compiler's own output — the same binary
// `make selfhost-cli` produces.
func selfHostCompiler(t *testing.T) string {
	t.Helper()
	selfHostOnce.Do(func() {
		fern := e2eharness.BuildLangBinForInterp(t)
		dir, err := os.MkdirTemp("", "fern-coreutils-selfhost-")
		if err != nil {
			selfHostFail = err.Error()
			return
		}
		bin := filepath.Join(dir, "fern-selfhost")
		src := filepath.Join(repoRoot(t), "examples", "self_host", "fern.fern")
		out, cerr := exec.Command(fern, "-target", fernTarget(t), "-o", bin, src).CombinedOutput()
		if cerr != nil {
			selfHostFail = string(out)
			return
		}
		selfHostPath = bin
	})
	if selfHostPath == "" {
		t.Fatalf("build the self-host compiler: %s", selfHostFail)
	}
	return selfHostPath
}

// selfHostBin compiles coreutils/<util>.fern with the self-host compiler.
//
// FERN_STRICT_IR=1 is the point of the exercise: it names the function that
// failed to lower instead of leaving a whole-module refusal to be read off a
// downstream symptom, and it is what turns "the self-host cannot compile this
// tree" into a message a reader can act on.
func selfHostBin(t *testing.T, util string) string {
	t.Helper()
	selfHostBinsMu.Lock()
	defer selfHostBinsMu.Unlock()
	if bin, ok := selfHostBins[util]; ok {
		return bin
	}
	if selfHostBinDir == "" {
		dir, err := os.MkdirTemp("", "fern-coreutils-selfhost-bin-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		selfHostBinDir = dir
	}
	root := repoRoot(t)
	bin := filepath.Join(selfHostBinDir, util)
	cmd := exec.Command(selfHostCompiler(t), "-target", fernTarget(t),
		filepath.Join(root, "coreutils", util+".fern"),
		filepath.Join(root, "internal", "stdlib"), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host compile coreutils/%s.fern: %v\n%s", util, err, out)
	}
	selfHostBins[util] = bin
	return bin
}

// TestSelfHostCoreutilsParity runs every utility's corpus against its
// self-host build and its native build and requires the two to agree.
func TestSelfHostCoreutilsParity(t *testing.T) {
	corpus := corpusByUtil()
	for _, util := range utilNames(t) {
		cases, ok := corpus[util]
		if !ok {
			continue // reported by TestSelfHostCoreutilsCoverage
		}
		t.Run(util, func(t *testing.T) {
			native := fernBin(t, util)
			ours := selfHostBin(t, util)
			for _, inv := range cases(t) {
				t.Run(inv.name, func(t *testing.T) {
					// Two processes per case and almost all of the cost
					// in spawning them, so the cases run concurrently —
					// this leg would otherwise double the package's wall
					// time on its own. Each case is its own pair of
					// processes with no shared state; the two binary
					// caches they read are mutex-guarded.
					t.Parallel()
					inv.prep(t)
					want := inv.run(t, native, util)
					wantFiles := inv.readArtifacts(t)
					inv.prep(t)
					got := inv.run(t, ours, util)
					diffArtifacts(t, util, inv, wantFiles, inv.readArtifacts(t), "native", "selfhost")
					if !bytes.Equal(want.stdout, got.stdout) {
						t.Errorf("stdout differs for %s %s\n  native: %s\nselfhost: %s", util, quoteArgs(inv.args), quote(want.stdout), quote(got.stdout))
					}
					if !bytes.Equal(want.stderr, got.stderr) {
						t.Errorf("stderr differs for %s %s\n  native: %s\nselfhost: %s", util, quoteArgs(inv.args), quote(want.stderr), quote(got.stderr))
					}
					if want.how() != got.how() {
						t.Errorf("status differs for %s %s: native %s, selfhost %s", util, quoteArgs(inv.args), want.how(), got.how())
					}
				})
			}
		})
	}
}
