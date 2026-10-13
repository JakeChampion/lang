package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// selfHostPassSwitches are the self-host compiler's switches that turn one
// optimisation off. Each is meant to be invisible to the program, so a
// generated program must give the interpreter's answer under any subset of
// them; until the differential legs set them, only hand-written tests did.
var selfHostPassSwitches = []string{
	"FERN_SELFHOST_NO_REUSE=1", // no construction builds in a donor's box
	"FERN_SEM_REUSE=",          // every unit planned and lowered afresh, none replayed
	"FERN_SEM_INLINE=",         // no inlining in the typed lowering
	"FERN_NO_ARRAY_FUSION=1",
	"FERN_NO_SCALE_KERNEL=1",
	"FERN_NO_PRODUCT_KERNEL=1",
}

// selfHostPassConfig is the set of switches seed is compiled under. Half the
// seeds get none, so the configuration that ships keeps most of the corpus;
// the rest set each switch with probability one half. It depends on the seed
// alone, so a failing seed reproduces with the configuration its log names.
func selfHostPassConfig(seed uint64) []string {
	x := seed ^ 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	if x&1 == 0 {
		return nil
	}
	var env []string
	for i, sw := range selfHostPassSwitches {
		if x>>(i+1)&1 == 1 {
			env = append(env, sw)
		}
	}
	return env
}

// logPassConfig records the switches a seed's compile runs under, so a
// failure's log carries what reproducing it needs.
func logPassConfig(t *testing.T, env []string) {
	t.Helper()
	if len(env) > 0 {
		t.Logf("pass configuration: %s", strings.Join(env, " "))
	}
}

// selfHostCompileCmd runs the self-host CLI with env added to this process's
// environment.
func selfHostCompileCmd(fernBin string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(fernBin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

func TestSelfHostPassConfigMix(t *testing.T) {
	const n = 512
	none, seen := 0, map[string]int{}
	for seed := uint64(0); seed < n; seed++ {
		env := selfHostPassConfig(seed)
		if len(env) == 0 {
			none++
		}
		for _, sw := range env {
			seen[sw]++
		}
	}
	if none < n/3 || none > 2*n/3 {
		t.Errorf("%d of %d seeds run the default configuration, want about half", none, n)
	}
	for _, sw := range selfHostPassSwitches {
		if seen[sw] < n/8 {
			t.Errorf("%s is set on %d of %d seeds, want about a quarter", sw, seen[sw], n)
		}
	}
}

// passEnv is passes(seed), or no switches when the leg varies none.
func passEnv(passes func(uint64) []string, seed uint64) []string {
	if passes == nil {
		return nil
	}
	return passes(seed)
}
