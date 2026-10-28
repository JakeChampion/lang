package e2ecompiler

import "testing"

// A successful address parse allocates what its result holds and nothing
// else: the error of a failed parse is built only on the failure path. The
// counts are those of the parsers before they reported a typed error.
func TestSelfHostNetParseAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := `import "std/net";
function ip_allocs(): i64 {
  let kept: i32 = 0;
  let before: i64 = __heap_alloc_count();
  for i in 0..100 {
    match (net.ip_parse("8.8.8.8")) {
      Ok(a) => { kept = kept + a.bytes().len(); },
      Err(_) => { return 0 - 1i64; }
    }
  }
  if (kept != 400) { return 0 - 2i64; }
  return (__heap_alloc_count() - before) / 100i64;
}
function socket_allocs(): i64 {
  let kept: i32 = 0;
  let before: i64 = __heap_alloc_count();
  for i in 0..100 {
    match (net.socket_addr_parse("127.0.0.1:8080")) {
      Ok(a) => { kept = kept + a.port; },
      Err(_) => { return 0 - 1i64; }
    }
  }
  if (kept != 808000) { return 0 - 2i64; }
  return (__heap_alloc_count() - before) / 100i64;
}
function main(): i32 {
  return (ip_allocs() * 10i64 + socket_allocs()) as i32;
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1")
			if code != 24 {
				t.Fatalf("exited %d, want 24 (ip_parse allocations * 10 + socket_addr_parse allocations)\n%s", code, stderr)
			}
		})
	}
}
