package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// client_received tests its parameter and then the field it reuses, so the
// field has a reuse token before the drop of the parameter takes the field
// out of its slot. The take gives the unit to the token when the token is
// the box, and to the field when the token is zero (#16803).
const takeAfterTokenSrc = `struct Quic { keys: u8[], n: i64 }

function (q: Quic) receive(d: u8[], now: i64): Quic {
  return Quic { ...q, n: q.n + d.len() as i64 + now };
}

struct Conn { quic: Quic, tags: string[], started: boolean, stopping: boolean, max_body: i64, parts: i64[], next: i64, parked: i64[], events: string[], date: string, date_line: u8[] }

function (c: Conn) receive(d: u8[], now: i64): Conn {
  return Conn { ...c, quic: c.quic.receive(d, now) };
}

struct Ends { client: Conn, server: Option[Conn], now: i64, log: string, lose_every: i32, carried: i32, reorder: boolean, requests: string[], ids: i64[], responses: string, window: i64, raw: boolean, tickets: boolean, wall: i64 }

@noinline
function client_received(e: Ends, d: u8[]): Conn {
  if (e.raw) {
    return Conn { ...e.client, quic: e.client.quic.receive(d, e.now) };
  }
  return e.client.receive(d, e.now);
}

function main(): i32 {
  let c = Conn { quic: Quic { keys: [1u8], n: 0 as i64 }, tags: ["a"], started: false, stopping: false, max_body: 0 as i64, parts: [], next: 0 as i64, parked: [], events: [], date: "d", date_line: [] };
  let cur = Ends { client: c, server: None, now: 1 as i64, log: "", lose_every: 0, carried: 0, reorder: false, requests: [], ids: [], responses: "", window: 0 as i64, raw: true, tickets: false, wall: 0 as i64 };
  let ds: u8[][] = [[2u8], [3u8], [4u8]];
  for d in ds {
    cur = Ends { ...cur, client: client_received(cur, d) };
    cur = Ends { ...cur, raw: !cur.raw };
  }
  print(cur.client.quic.n.to_string());
  return 0;
}
`

func TestSelfHostCertifiesATakeAfterTheReuseToken(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, takeAfterTokenSrc, []string{"FERN_LEAKCHECK=1", "FERN_RC_CERTIFY_MEMO=0"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			if !strings.HasPrefix(string(out), "6") {
				t.Fatalf("want 6 on stdout:\n%s", out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("ownership: %v\n%s", err, out)
			}
		})
	}
}
