package fernsmith_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

const emiSample = `function sum(xs: i32[]): i32 {
  let total: i32 = 0;
  for x in xs {
    if (x < 0) {
      continue;
    }
    total = total + x;
  }
  return total;
}

function main(): i32 {
  let n: i32 = 0;
  let i: i32 = 0;
  while (i < 4) {
    n = n + sum([i, 0 - i, 2]);
    i = i + 1;
  }
  assert(n == 14);
  return n - 14;
}
`

var (
	emiDead = regexp.MustCompile(`if \(emi_false\(\)\) \{\n\s*(let|total|n|i|if|return|for|while)\b`)
	emiLive = regexp.MustCompile(`if \(emi_true\(\)\) \{`)
	emiJump = regexp.MustCompile(`if \(emi_false\(\)\) \{\n\s*(break|continue);`)
	emiOnce = regexp.MustCompile(`loop \{\n[^\n]*\n\s*break;\n\s*\}`)
)

func TestEMIReachesEachStep(t *testing.T) {
	valid := func(src string) bool { return checkGenerated(t, src) == nil }
	seen := map[string]int{}
	for seed := uint64(0); seed < 64; seed++ {
		src, kept := fernsmith.EMI(emiSample, seed, 6, valid)
		if kept == 0 {
			continue
		}
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("seed %d: the mutant does not check: %v\n%s", seed, err, src)
		}
		if n := strings.Count(src, "function emi_false("); n != 1 {
			t.Fatalf("seed %d: %d declarations of emi_false\n%s", seed, n, src)
		}
		for name, re := range map[string]*regexp.Regexp{"dead": emiDead, "live": emiLive, "jump": emiJump, "once": emiOnce} {
			if re.MatchString(src) {
				seen[name]++
			}
		}
	}
	for _, name := range []string{"dead", "live", "jump", "once"} {
		if seen[name] == 0 {
			t.Errorf("no seed made a %s step: %v", name, seen)
		}
	}
}

func TestEMIKeepsOnlyValidSteps(t *testing.T) {
	if src, kept := fernsmith.EMI(emiSample, 1, 8, func(string) bool { return false }); kept != 0 || src != emiSample {
		t.Fatalf("kept %d steps that valid rejected:\n%s", kept, src)
	}
	taken := "function emi_true(): boolean { return false; }\n" + emiSample
	if src, kept := fernsmith.EMI(taken, 1, 8, func(string) bool { return true }); kept != 0 || src != taken {
		t.Fatalf("mutated a program that declares a guard name itself:\n%s", src)
	}
}
