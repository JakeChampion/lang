package coreutils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestUtilitiesFreeWhatTheyAllocate builds utilities with FERN_LEAKCHECK and
// requires each invocation to free every allocation it made. The parity
// suites compare output with GNU and cannot see a leak; `ls -l` and `stat`
// leaked the string builder behind every mode column they printed, and every
// directory listing lost 8 bytes per name whose length is a multiple of 8.
func TestUtilitiesFreeWhatTheyAllocate(t *testing.T) {
	// A name of every length from 1 to 33: a name buffer sized off by a byte
	// is freed into the wrong size class only when its length is a multiple
	// of 8.
	dir := t.TempDir()
	for n := 1; n <= 33; n++ {
		seedWrite(t, dir, strings.Repeat("f", n), "contents\n")
	}
	seedMkdir(t, dir, "sub")
	for _, c := range []struct {
		name string
		util string
		args []string
	}{
		{"ls_-l", "ls", []string{"-l", dir}},
		{"ls_-la", "ls", []string{"-la", dir}},
		{"stat", "stat", []string{filepath.Join(dir, "f"), filepath.Join(dir, "ffffffff"), filepath.Join(dir, "sub")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			argv := crossArgv(leakcheckBin(t, c.util), c.args...)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = baseEnv()
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%s failed: %v\n%s", c.util, err, stderr.String())
			}
			allocs, frees, live, exited, ok := census(stderr.Bytes())
			if !ok || exited {
				t.Fatalf("no leakcheck summary from a return from main:\n%s", stderr.String())
			}
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want every allocation freed", allocs, frees, live)
			}
		})
	}
}

var (
	leakcheckBinsMu sync.Mutex
	leakcheckBins   = map[string]string{}
	leakcheckBinDir string
)

// leakcheckBin compiles coreutils/<util>.fern with FERN_LEAKCHECK, which the
// compiler reads at emit time, so it is a separate build from fernBin's; once
// per test process, like fernBin's.
func leakcheckBin(t *testing.T, util string) string {
	t.Helper()
	leakcheckBinsMu.Lock()
	defer leakcheckBinsMu.Unlock()
	if bin, ok := leakcheckBins[util]; ok {
		return bin
	}
	if leakcheckBinDir == "" {
		dir, err := os.MkdirTemp("", "fern-coreutils-leakcheck-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		leakcheckBinDir = dir
	}
	root := repoRoot(t)
	e2eharness.TrackFernSources(t, filepath.Join(root, "coreutils"), util+".fern")
	bin := filepath.Join(leakcheckBinDir, util)
	cmd := exec.Command(e2eharness.BuildLangBinForInterp(t), "-target", fernTarget(t), "-o", bin,
		filepath.Join(root, "coreutils", util+".fern"))
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s with FERN_LEAKCHECK: %v\n%s", util, err, out)
	}
	leakcheckBins[util] = bin
	return bin
}

// census is the leakcheck line a run under FERN_LEAKCHECK prints as it exits,
// parsed: the counts, whether the exit() builtin printed it (` from=exit`)
// rather than a return from main, and whether there was one. The line is
// found wherever it starts: a prompt the utility left unterminated puts it
// mid-line.
func census(stderr []byte) (allocs, frees, live int64, exited, ok bool) {
	i := strings.LastIndex(string(stderr), "leakcheck: allocs=")
	if i < 0 {
		return
	}
	line := string(stderr[i:])
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	if _, err := fmt.Sscanf(line, "leakcheck: allocs=%d frees=%d live_bytes=%d", &allocs, &frees, &live); err != nil {
		return
	}
	return allocs, frees, live, strings.HasSuffix(line, " from=exit"), true
}

// TestCorpusFreesWhatItAllocates runs every utility's corpus — the same
// cases the GNU parity gate runs — against a build with FERN_LEAKCHECK and
// requires each run that returned from main to have freed every allocation
// it made. The parity gate compares output and cannot see a leak; this is
// the leak gate over the largest programs in the tree after the compiler.
//
// Three kinds of run cannot be held to that. A run the exit() builtin ended
// (the census says ` from=exit`) never unwound the frames above the call,
// so what they held is not something the program could have freed: the
// usage errors, `--help` and `--version`, and the diagnostics that leave
// through `gnu.die`. A run that exec'd another program (env, nice, nohup,
// chroot) left no census at all, its frames abandoned the same way. A run a
// signal ended has no exit to report at: a `tail -f` the harness stops with
// SIGTERM, a writer killed by SIGPIPE on a closed pipe, a case that overran
// its bound. All three are counted, not checked, and a utility none of whose
// cases returned fails when one of them left through exit() with status 0
// rather than passing on nothing: its success path leaves that way, and the
// census can say nothing about it until it returns. A utility whose every
// case failed has no success path to judge here, as chroot unprivileged,
// where chroot(2) is EPERM before anything runs.
func TestCorpusFreesWhatItAllocates(t *testing.T) {
	for _, util := range utilNames(t) {
		cases, ok := corpusRegistry[util]
		if !ok {
			continue // reported by TestSelfHostCoreutilsCoverage
		}
		t.Run(util, func(t *testing.T) {
			bin := leakcheckBin(t, util)
			var checked, exited, exitedOK, execd, unended int64
			for _, inv := range cases(t) {
				t.Run(inv.name, func(t *testing.T) {
					// One process per case and the cost in spawning it, so the
					// cases run concurrently where the self-host leg's do.
					if inv.stdoutPath == "" && len(inv.follow) == 0 &&
						inv.prepare == nil && len(inv.artifacts) == 0 {
						t.Parallel()
					}
					inv := inv.own(t)
					inv.prep(t)
					got := inv.run(t, bin, util)
					if got.signal != "" || got.overran || got.flooded {
						atomic.AddInt64(&unended, 1)
						return
					}
					out := got.stderr
					if inv.merged {
						out = got.stdout
					}
					allocs, frees, live, viaExit, ok := census(out)
					if !ok {
						atomic.AddInt64(&execd, 1)
						return
					}
					if viaExit {
						atomic.AddInt64(&exited, 1)
						if got.exit == 0 {
							atomic.AddInt64(&exitedOK, 1)
						}
						return
					}
					atomic.AddInt64(&checked, 1)
					if allocs != frees || live != 0 {
						t.Errorf("%s %s: allocs=%d frees=%d live_bytes=%d, want every allocation freed", util, quoteArgs(inv.args), allocs, frees, live)
					}
				})
			}
			t.Cleanup(func() {
				t.Logf("%s: %d runs returned from main and balanced, %d left through exit(), %d exec'd, %d ended in a signal", util, checked, exited, execd, unended)
				if checked == 0 && execd == 0 && unended == 0 && exitedOK > 0 {
					t.Errorf("none of %s's cases returned from main, and %d of the %d that left through exit() succeeded: its success path must return from main for the census to say anything", util, exitedOK, exited)
				}
			})
		})
	}
}
