package e2ecompiler

import "testing"

// An instance of a generic function is inlined as its declaration is. Then
// the error that ok_or is given is built only on the None path.
const genericInlineSrc = `import "std/net";
import "std/option";
struct E { text: string, n: i32 }
@noinline function find(s: string): Option[i32] {
  if (s.len() > 3) { return Some(s.len()); }
  return None;
}
@noinline function parse(s: string): Result[i32, E] {
  return find(s).ok_or(E { text: s, n: 2 });
}
function main(): i32 {
  let before: i64 = __heap_alloc_count();
  let sum: i32 = 0;
  for i in 0..1000 {
    if let Ok(v) = parse("8.8.8.8") { sum = sum + v; }
  }
  if (__heap_alloc_count() - before > 10i64) { return 1; }
  if let Err(e) = parse("ab") {
    if (e.text != "ab" || e.n != 2) { return 2; }
  } else {
    return 3;
  }
  before = __heap_alloc_count();
  for i in 0..1000 {
    if let Ok(ip) = net.ip_parse("8.8.8.8") { sum = sum + 1; }
  }
  if (__heap_alloc_count() - before > 1010i64) { return 4; }
  if (sum != 8000) { return 5; }
  return 0;
}
`

func TestSelfHostGenericInstanceInlined(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, genericInlineSrc, target, "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("exit %d (1 = ok_or allocates on Some, 2 and 3 = the error, 4 = ip_parse allocates, 5 = a result)\n%s", code, stderr)
			}
		})
	}
}
