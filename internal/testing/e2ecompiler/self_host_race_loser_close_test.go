package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// raceLoserSource races a fast upstream against a slow one five times. The
// native leg returns how many more descriptors are open after the races than
// before: each loser's socket used to stay open, one per race.
const raceLoserSource = `import "std/async";
import "std/fetch";

function parse(s: string): i32 {
    let n: i32 = 0; let i: i32 = 0;
    while (i < s.len()) { n = n * 10 + (s[i] as i32 - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }
function open_fds(): i32 {
    match (read_dir("/proc/self/fd")) { Ok(names) => { return names.len(); }, Err(_) => { return 0; } }
}

function main(): i32 {
    let host: i32 = 127 | (1 << 24);
    let none: u8[] = [];
    let before: i32 = open_fds();
    let k: i32 = 0;
    while (k < 5) {
        let fs: async.Future[u8[]][] = [fetch.fetch_future(host, port("PA"), "/a"), fetch.fetch_future(host, port("PB"), "/b")];
        let (winner, body) = async.race(fs, none);
        if (winner != 0 || body.len() != 3) { return 90; }
        k = k + 1;
    }
    return open_fds() - before;
}
`

// TestSelfHostRaceClosesTheLoser requires `race` to close each loser's socket:
// natively the program ends with no more descriptors open than it started
// with, and as a wasm component its census balances.
func TestSelfHostRaceClosesTheLoser(t *testing.T) {
	cli := buildSelfHostCLI(t)
	pA := e2eharness.StartDelayedUpstream(t, "AAA", 5*time.Millisecond)
	pB := e2eharness.StartDelayedUpstream(t, "BBB", 300*time.Millisecond)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(raceLoserSource), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"PA=" + strconv.Itoa(pA), "PB=" + strconv.Itoa(pB)}

	t.Run("x86_64", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src))
		cmd.Env = append(os.Environ(), env...)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("exit %d: %d descriptors left open after five races (90 = the fast upstream lost)", code, code)
		}
	})

	t.Run("wasm_component", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
		component := cli.wasmComponent(t, src, "FERN_LEAKCHECK=1")
		args := []string{"run", "-S", "inherit-network"}
		for _, kv := range env {
			args = append(args, "--env", kv)
		}
		cmd := exec.Command(wasmtime, append(args, component)...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr.String())
		}
		assertBalancedCensus(t, stderr.String())
	})
}
