package e2eharness

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

const FramingAllocsRounds = 1000

func FramingAllocsSource() string {
	return fmt.Sprintf(`import "std/bench";
import "std/http";

// keep stands for the handler a server hands the request to, so the parse
// builds everything a server keeps however much of it this probe reads.
@noinline function keep(f: http.HttpFramed): i32 { return f.len; }

// The parse is called from two places, as std/serve calls it from six, so
// what it returns crosses a call rather than being spliced into its caller.
function parse_once(buf: u8[], limits: http.HttpLimits, prev: http.HttpFramed): http.HttpFramed {
  match (http.http_parse_request_framed_from(buf, 0, limits, prev)) {
    http.Framed(f) => { return f; },
    _ => { return http.http_framed_none(); }
  }
}

function parse_first(buf: u8[], limits: http.HttpLimits): http.HttpFramed {
  match (http.http_parse_request_framed_from(buf, 0, limits, http.http_framed_none())) {
    http.Framed(f) => { return f; },
    _ => { return http.http_framed_none(); }
  }
}

function serialize_once(out: usize, resp: HttpResponse): i32 {
  http.http_serialize_response_into(out, "GET", "HTTP/1.1", resp, true, "");
  let n: i32 = buf_len(out);
  buf_clear(out);
  return n;
}

function main(): i32 {
  let buf: u8[] = "GET / HTTP/1.1\r\nHost: localhost:8080\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\n\r\n".bytes();
  let limits: http.HttpLimits = http.http_limits();
  let resp: HttpResponse = http.ok("hello");
  let out: usize = buf_new(16);
  let f: http.HttpFramed = parse_first(buf, limits);
  let parsed: i32 = keep(f);
  let wire: i32 = serialize_once(out, resp);
  let a0: i64 = bench.alloc_count();
  let i: i32 = 0;
  while (i < %[1]d) {
    f = parse_once(buf, limits, http.http_framed_kept(f));
    parsed = parsed + keep(f);
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  i = 0;
  while (i < %[1]d) {
    wire = wire + serialize_once(out, resp);
    i = i + 1;
  }
  let a2: i64 = bench.alloc_count();
  buf_free(out);
  eprint("parsed=" + parsed.to_string() + " wire=" + wire.to_string() + " parse=" + (a1 - a0).to_string() + " serialize=" + (a2 - a1).to_string());
  return 0;
}
`, FramingAllocsRounds)
}

type FramingAllocs struct{ Parse, Serialize int64 }

const (
	framingRequestBytes = 77
	framingReplyBytes   = 43
)

var framingAllocsLine = regexp.MustCompile(`parsed=(\d+) wire=(\d+) parse=(\d+) serialize=(\d+)`)

func CheckFramingAllocs(t *testing.T, out string, want FramingAllocs) {
	t.Helper()
	m := framingAllocsLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("framing probe printed no report:\n%s", out)
	}
	n := make([]int64, 4)
	for i := range n {
		n[i], _ = strconv.ParseInt(m[i+1], 10, 64)
	}
	rounds := int64(FramingAllocsRounds + 1)
	if n[0] != rounds*framingRequestBytes || n[1] != rounds*framingReplyBytes {
		t.Fatalf("framing probe did not frame every round: parsed %d bytes and wrote %d, want %d and %d",
			n[0], n[1], rounds*framingRequestBytes, rounds*framingReplyBytes)
	}
	got := FramingAllocs{Parse: perRound(t, "parse", n[2]), Serialize: perRound(t, "serialize", n[3])}
	if got == want {
		return
	}
	t.Errorf("framing path allocations per request: parse %d, serialize %d; pinned parse %d, serialize %d.\n"+
		"Above the pin is a regression. Below it, lower the pin in the same change.",
		got.Parse, got.Serialize, want.Parse, want.Serialize)
}

func perRound(t *testing.T, what string, total int64) int64 {
	t.Helper()
	if total%FramingAllocsRounds != 0 {
		t.Fatalf("%s allocated %d over %d rounds, not a whole number per round", what, total, FramingAllocsRounds)
	}
	return total / FramingAllocsRounds
}
