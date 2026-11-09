package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The three forms of a byte view: an owned array, a slice of one, and the
// bytes of a string.
const bytesDataProg = `function sum(p: usize, n: i32): i32 {
  let t = 0;
  unsafe {
    for i in 0..n {
      t += __load_u8(p + i as usize);
    }
  }
  return t;
}

@noinline function view_sum(b: [u8]): i32 {
  unsafe {
    return sum(__bytes_data(b), b.len());
  }
}

function main(): i32 {
  let xs: u8[] = [];
  for i in 0..10 {
    xs = xs.append((i + 1) as u8);
  }
  let s = "hello, world";
  let owned = unsafe { sum(__bytes_data(xs), xs.len()) };
  print(view_sum(xs).to_string() + " " + view_sum(xs[2:5]).to_string() + " " + view_sum(s.as_bytes()).to_string() + " " + owned.to_string() + " " + view_sum([]).to_string());
  return 0;
}
`

// __bytes_data gives the address of the bytes of each form of a byte view.
func TestSelfHostBytesDataEveryTarget(t *testing.T) {
	h := selfHostCLIForHost(t)
	h.cli = e2eharness.RuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, _ := routedMapRun(t, h.cli, h.stdlib, bytesDataProg, target)
			if got != "55 12 1160 55 0" {
				t.Fatalf("%s: got %q, want %q", target, got, "55 12 1160 55 0")
			}
		})
	}
}
