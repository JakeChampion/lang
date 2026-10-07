package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Exercise the production scheduler with deterministic candidate outcomes.
// Real filesystem, random alphabet and GNU parity checks remain in coreutils.
func TestSelfHostMktempRetry(t *testing.T) {
	source, err := os.ReadFile("../../../coreutils/mktemp.fern")
	if err != nil {
		t.Fatal(err)
	}
	const mainDecl = "function main(): i32"
	if strings.Count(string(source), mainDecl) != 1 {
		t.Fatal("mktemp entry point changed")
	}
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "lib"), os.DirFS("../../../coreutils/lib")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mktemp-retry.fern")
	// Copy the actual production loop, changing only its IO expression and
	// limit input. Keep the loop itself verbatim, without adding a production
	// callback allocation just for fault injection.
	const makeDecl = "function make(s: Settings, head: string, run: i32, tail: string): Attempt {"
	start := strings.Index(string(source), makeDecl)
	if start < 0 {
		t.Fatal("mktemp make boundary changed")
	}
	end := strings.Index(string(source)[start:], "\n}\n")
	if end < 0 {
		t.Fatal("mktemp make body changed")
	}
	loop := string(source)[start : start+end+3]
	for _, replacement := range [][2]string{
		{makeDecl, "function retry(limit: i32, candidate: (i32) => (Attempt, boolean)): Attempt {"},
		{"while (tries < attempt_limit())", "while (tries < limit)"},
		{"try_once(s, head + random_run(run) + tail)", "candidate(tries)"},
	} {
		if strings.Count(loop, replacement[0]) != 1 {
			t.Fatalf("mktemp retry boundary changed: %s", replacement[0])
		}
		loop = strings.Replace(loop, replacement[0], replacement[1], 1)
	}
	src := strings.Replace(string(source), mainDecl, "function production_main(): i32", 1) + "\n" + loop + mktempRetryChecks
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Run("arm64-darwin", func(t *testing.T) {
			project := t.TempDir()
			copySelfHostDriver(t, project, "fern.fern")
			cli := buildSelfHostBinArm64Darwin(t, project, "fern.fern", "fern")
			for _, full := range []bool{false, true} {
				bin := filepath.Join(dir, "retry")
				cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, path, e2eharness.SelfHostStdlibRoot(t))
				env, args := mktempRetryMode(full)
				cmd.Env = e2eharness.SelfHostChildEnv(env...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile full=%v: %v\n%s", full, err, out)
				}
				out, err := exec.Command(bin, args...).CombinedOutput()
				if err != nil {
					t.Fatalf("run full=%v: %v\n%s", full, err, out)
				}
				assertBalancedCensus(t, string(out))
			}
		})
	} else {
		cli := buildSelfHostCLI(t)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target, func(t *testing.T) {
				for _, full := range []bool{false, true} {
					env, args := mktempRetryMode(full)
					stderr, code := cli.exitOfFileArgs(t, path, target, nil, args, env...)
					if code != 0 {
						t.Fatalf("full=%v exit %d: %s", full, code, stderr)
					}
					assertBalancedCensus(t, stderr)
				}
			})
		}
	}
}

func mktempRetryMode(full bool) (env, args []string) {
	env = []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}
	if full {
		// The sanitizer quarantines freed blocks. Hundreds of millions of
		// attempts must recycle storage; use the balanced census here and
		// retain sanitizer coverage on every boundary at the smaller scale.
		args = []string{"full"}
	} else {
		env = append(env, "FERN_SANITIZE=1")
	}
	return env, args
}

const mktempRetryChecks = `
function check_retry(limit: i32, stop: i32, success: boolean): void {
  let seen: i32 = 0;
  function candidate(at: i32): (Attempt, boolean) {
    seen = seen + 1;
    assert(at == seen);
    assert(at <= limit);
    if (at == stop) {
      return (Attempt { name: "last", ok: success, text: "terminal" }, false);
    }
    let name: string = "early";
    if (at == limit) { name = "last"; }
    return (Attempt { name: name, ok: false, text: "collision" }, true);
  }
  let r: Attempt = retry(limit, candidate);
  if (limit <= 0) {
    assert(seen == 0 && !r.ok && r.name == "" && r.text == "File exists");
  } else if (stop > 0 && stop <= limit) {
    assert(seen == stop && r.ok == success && r.name == "last" && r.text == "terminal");
  } else {
    assert(seen == limit && !r.ok && r.name == "last" && r.text == "collision");
  }
}
function main(): i32 {
  check_retry(0, 0, false);
  check_retry(-1, 0, false);
  check_retry(1, 1, true);
  check_retry(7, 1, true);
  check_retry(7, 3, true);
  check_retry(7, 7, true);
  check_retry(7, 1, false);
  check_retry(7, 3, false);
  check_retry(7, 7, false);
  check_retry(1, 0, false);
  check_retry(7, 0, false);
  check_retry(7, 8, true);
  if (target_os() == "darwin") {
    assert(attempt_limit() == 308915776);
  } else {
    assert(attempt_limit() == 238328);
  }
  let a: string[] = args();
  if (a.len() > 1 && a[1] == "full") {
    check_retry(attempt_limit(), 0, false);
  } else {
    check_retry(1000, 0, false);
  }
  return 0;
}
`
