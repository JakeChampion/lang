package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// credit_error is a method of more than one block with one call site, and
// it calls a function, so it is not a leaf. It is spliced into on_stream, and
// the update still takes the element across the joins that its returns make
// (#14440). As a call it lends cur, and the count is the same.
const splicedBranchingMethodProg = `import "std/array";

struct Stream { data: u8[], highest: i64, recv_max: i64, reset: i64, fin: boolean }
struct Streams { list: Stream[], recv_highest: i64, recv_max: i64, failed: i64, events: i64[] }

@noinline
function window(r: Stream): i64 {
  return r.recv_max;
}

function (s: Streams) credit_error(r: Stream, end: i64): Option[i64] {
  if (end > window(r)) {
    return Some(1 as i64);
  }
  let more = end - r.highest;
  if (s.recv_highest + more > s.recv_max) {
    return Some(2 as i64);
  }
  return None;
}

@noinline
function find(s: Streams, id: i64): (Streams, i32) {
  return (Streams { ...s, failed: 0 }, (id % 4) as i32);
}

@noinline
function (st: Stream) received(data: [u8], more: i64, keep: boolean): Stream {
  if (!keep) {
    return Stream { ...st, highest: st.highest + more };
  }
  let out = st.data;
  for b in data {
    out = out.append(b);
  }
  return Stream { ...st, data: out, highest: st.highest + more };
}

@noinline
function on_stream(s: Streams, id: i64, data: [u8]): Streams {
  let (cur, i) = find(s, id);
  if (i < 0) {
    return cur;
  }
  let st = cur.list[i];
  let end = st.highest + data.len() as i64;
  if let Some(e) = cur.credit_error(st, end) {
    return Streams { ...cur, failed: e };
  }
  let more = end - st.highest;
  let next = Streams { ...cur, recv_highest: cur.recv_highest + more };
  if (st.reset >= 0) {
    return Streams { ...next, list: next.list.with(i, next.list[i].received(data, more, false)) };
  }
  let before = st.data.len();
  let fin = st.fin;
  next = Streams { ...next, list: next.list.with(i, next.list[i].received(data, more, true)) };
  if (next.list[i].data.len() > before && !fin) {
    return Streams { ...next, events: next.events.append(id) };
  }
  return next;
}

function main(): i32 {
  let list: Stream[] = [];
  for j in 0..4 {
    list = list.append(Stream { data: [], highest: 0, recv_max: 1000000, reset: -1, fin: false });
  }
  let s = Streams { list: list, recv_highest: 0, recv_max: 100000000, failed: 0, events: [] };
  let chunk: u8[] = [1 as u8, 2 as u8, 3 as u8];
  let before = __heap_alloc_count();
  for round in 0..1000 {
    s = Streams { ...on_stream(s, round as i64, chunk), events: [] };
  }
  let n = __heap_alloc_count() - before;
  if (s.list[0].data.len() != 750 || s.list[3].highest != 750 as i64 || s.recv_highest != 3000 as i64 || s.failed != 0 as i64) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n > 1100 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostSplicedBranchingMethod(t *testing.T) {
	runRereadProgram(t, "spliced_branching_method.fern", splicedBranchingMethodProg)

	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "spliced_branching_method.fern")
	if err := os.WriteFile(src, []byte(splicedBranchingMethodProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		path := filepath.Join(filepath.Dir(src), target+".s")
		if out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
		asm, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		call := "call __fn_Streams__credit_error"
		if target == "arm64-linux" {
			call = "bl __fn_Streams__credit_error"
		}
		if body := condBranchBody(t, string(asm), "on_stream"); strings.Contains(body, call) {
			t.Errorf("%s: on_stream still calls Streams.credit_error", target)
		}
	}
}
