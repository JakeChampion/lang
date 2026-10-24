package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const optBisectSrc = `enum L { Nil, Cons(i32, L) }

function build(n: i32): L {
  if (n == 0) {
    return Nil;
  }
  return Cons(n, build(n - 1));
}

function incr(l: L): L {
  match (l) {
    Cons(x, rest) => {
      return Cons(x + 1, incr(rest));
    },
    Nil => {
      return Nil;
    }
  }
}

function total(l: L): i32 {
  match (l) {
    Cons(x, rest) => {
      return x + total(rest);
    },
    Nil => {
      return 0;
    }
  }
}

function main(): i32 {
  let l: L = build(20);
  let i: i32 = 0;
  while (i < 5) {
    l = incr(l);
    i = i + 1;
  }
  return total(l) & 255;
}
`

func TestSelfHostOptBisectFindsTheDecision(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no self-host target runs natively here")
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(optBisectSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := e2eharness.SelfHostCompileCmd(t, target, src, filepath.Join(t.TempDir(), "prog"))
	cmd.Env = e2eharness.SelfHostChildEnv("FERN_OPT_TRACE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("traced compile: %v\n%s", err, out)
	}
	ds := e2eharness.ParseOptTrace(string(out))
	traced := false
	for _, d := range ds {
		traced = traced || d.String() == "reuse incr"
	}
	if !traced {
		t.Fatalf("FERN_OPT_TRACE does not list `reuse incr`:\n%s", out)
	}

	allocs := func(env []string) int64 {
		t.Helper()
		bin := e2eharness.CompileSelfHostFile(t, target, src, append(env, "FERN_LEAKCHECK=1"))
		stderr, err := exec.Command(bin).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 54 {
			t.Fatalf("%v: %v, want exit 54:\n%s", env, err, stderr)
		}
		var n, frees, live int64
		if _, err := fmtSscan(leakSummaryLine(string(stderr)), &n, &frees, &live); err != nil || n != frees || live != 0 {
			t.Fatalf("%v: %v\n%s", env, err, stderr)
		}
		return n
	}
	reused, fresh := allocs(nil), allocs([]string{"FERN_SELFHOST_NO_REUSE=1"})
	if reused >= fresh {
		t.Fatalf("reuse saves nothing here (%d allocations, %d without it), so the probe tests nothing", reused, fresh)
	}
	got, ok := e2eharness.BisectOptDecisions(ds, func(bound uint64) bool {
		return allocs(e2eharness.OptBisectEnv(nil, bound)) == fresh
	})
	if !ok || got.String() != "reuse incr" {
		t.Fatalf("bisect named %v (found %v), want `reuse incr`; decisions %v", got, ok, ds)
	}
}
