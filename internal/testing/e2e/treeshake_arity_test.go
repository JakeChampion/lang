package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// treeshakeAritySrc has a method `add` with three parameters and a method
// `neg` with one, each of which calls a C import. The program calls `add`
// with one argument on an array of a generic element type, and uses `+`
// and unary minus. None of those can reach the two methods, so the C
// import is not reachable, and the program builds as an executable, which
// has no dynamic loader for a C import.
const treeshakeAritySrc = `import "std/array";

@import("libc.so.6", "getpid")
function c_getpid(): i32;

struct Pages { n: i32 }

// No call reaches these: a call of add with one argument and the plus
// operator need one parameter, and unary minus needs none.
pub function (p: Pages) add(w: i32, h: i32, d: i32): i32 {
  return c_getpid() + w + h + d;
}

pub function (p: Pages) neg(x: i32): i32 {
  return c_getpid() + x;
}

struct Money { cents: i64 }

pub function (a: Money) add(b: Money): Money {
  return Money { cents: a.cents + b.cents };
}

function main(): i32 {
  let xs = [1, 2, 3].add([4, 5, 6]);
  let m = Money { cents: 5i64 } + Money { cents: 7i64 };
  let s = "a" + "b";
  let k = -xs[0];
  print(s + " " + xs[2].to_string() + " " + m.cents.to_string() + " " + k.to_string());
  return 0;
}
`

func TestTreeshakeMethodArity(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skip("no self-host target runs natively on this host")
	}
	bin := e2eharness.CompileSelfHostSource(t, host, treeshakeAritySrc, nil)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "ab 9 12 -5" {
		t.Fatalf("got %q, want %q", got, "ab 9 12 -5")
	}
}
