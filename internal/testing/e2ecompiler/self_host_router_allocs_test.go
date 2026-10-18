package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// std/http/router's cost per request, by allocation count, on each target:
// a literal route must cost what its handler alone costs, and a route with
// one parameter that handler plus the parameter's copy, the values array
// and the Params record. The pin is a ratchet: a count above it is a
// regression, one below it an improvement whose PR lowers the pin.
func TestSelfHostRouterAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(routerAllocsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		want   routerAllocs
	}{
		{"x86-64-linux", routerAllocs{literal: 1, param: 7, bare: 1}},
		{"arm64-linux", routerAllocs{literal: 1, param: 7, bare: 1}},
		{"wasm32-wasi", routerAllocs{literal: 1, param: 8, bare: 1}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			out, code := cli.exitOfFile(t, src, tc.target, nil, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("router probe exited %d:\n%s", code, out)
			}
			checkRouterAllocs(t, out, tc.want)
		})
	}
}

type routerAllocs struct{ literal, param, bare int64 }

const routerAllocsRounds = 10000

var routerAllocsLine = regexp.MustCompile(`literal=(\d+) param=(\d+) bare=(\d+) status=(\d+)`)

func checkRouterAllocs(t *testing.T, out string, want routerAllocs) {
	t.Helper()
	m := routerAllocsLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("router probe printed no report:\n%s", out)
	}
	n := make([]int64, 4)
	for i := range n {
		n[i], _ = strconv.ParseInt(m[i+1], 10, 64)
	}
	if n[3] != 3*200*routerAllocsRounds {
		t.Fatalf("router probe did not answer every round 200: status sum %d", n[3])
	}
	per := func(name string, total int64) int64 {
		if total%routerAllocsRounds != 0 {
			t.Fatalf("%s allocated %d over %d rounds, not a whole number per round", name, total, routerAllocsRounds)
		}
		return total / routerAllocsRounds
	}
	got := routerAllocs{literal: per("literal", n[0]), param: per("param", n[1]), bare: per("bare", n[2])}
	if got == want {
		return
	}
	t.Errorf("router allocations per request: literal %d, param %d, bare handler %d; pinned %d, %d, %d.\n"+
		"Above the pin is a regression. Below it, lower the pin in the same change.",
		got.literal, got.param, got.bare, want.literal, want.param, want.bare)
}

// The probe routes two requests through a five-route table, one to a
// literal pattern and one to a pattern with a parameter whose value the
// handler reads, and calls the literal route's handler directly for the
// cost the handler alone has; each count is a total over the rounds.
const routerAllocsSource = `import "std/http";
import "std/http/router";
import "std/platform";

function show(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  return Ok(http.ok("user " + params.values[0]));
}

function hello(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  return Ok(http.ok("hi"));
}

function main(): i32 {
  let r: router.Router[platform.Host] = router.new[platform.Host]().get("/hello", hello).get("/users/{id}", show).post("/users", hello).get("/files/{path...}", hello).get("/health", hello);
  let plat: platform.Host = platform.host();
  let param_req: HttpRequest = http.request("GET", "/users/42");
  let literal_req: HttpRequest = http.request("GET", "/hello");
  let n: i32 = ` + "10000" + `;
  let status: i32 = 0;
  let i: i32 = 0;
  let before: i64 = __heap_alloc_count();
  while (i < n) {
    status = status + r.handle(param_req, plat).status;
    i = i + 1;
  }
  let after_param: i64 = __heap_alloc_count();
  i = 0;
  while (i < n) {
    status = status + r.handle(literal_req, plat).status;
    i = i + 1;
  }
  let after_literal: i64 = __heap_alloc_count();
  i = 0;
  while (i < n) {
    status = status + http.respond(hello(literal_req, router.no_params(), plat)).status;
    i = i + 1;
  }
  let after_bare: i64 = __heap_alloc_count();
  eprint("literal=" + (after_literal - after_param).to_string() + " param=" + (after_param - before).to_string() + " bare=" + (after_bare - after_literal).to_string() + " status=" + status.to_string());
  return 0;
}
`
