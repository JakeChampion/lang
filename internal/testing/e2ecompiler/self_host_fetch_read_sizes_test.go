package e2ecompiler

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A response read starts at 8 KiB and grows to 64 KiB. Each body here sits
// beside one of those steps, and every byte of it is checked.
func TestSelfHostFetchReadSizes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, n := range []int{1, 8191, 8192, 8193, 16385, 65536, 65537, 300001} {
		port := e2eharness.StartBodyUpstream(t, e2eharness.AlphabetBody(n))
		src := fmt.Sprintf(`import "std/fetch";
function main(): i32 {
  match (fetch.send(fetch.get("http://127.0.0.1:%d/"))) {
    Ok(resp) => {
      let body: u8[] = resp.body_bytes();
      if (body.len() != %d) { return 1; }
      let i: i32 = 0;
      while (i < body.len()) {
        if (body[i] != (97 + i %% 26) as u8) { return 2; }
        i = i + 1;
      }
      return 0;
    },
    Err(e) => { return 3; }
  }
  return 4;
}
`, port, n)
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(fmt.Sprintf("%d/%s", n, target), func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1"); code != 0 {
					t.Fatalf("exit %d (1 = length, 2 = a byte, 3 = fetch error)\n%s", code, stderr)
				}
			})
		}
	}
}
