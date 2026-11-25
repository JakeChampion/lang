package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The `?` of the spliced record merges the Ok record and the filler of each
// Err arm in one phi, and the Err arm hands the phi on whole. So the inliner
// cannot read the fields of the record off its construction. Threading then
// gives the loop body the construction itself, and the split after it removes
// the box of each Step (#14440). Each call allocates 5 boxes, not 6.
const threadedSplitProg = `struct Srv { key: u8[], seq: u64 }
struct Session { server: Srv, held: u8[] }
struct Step { session: Session, output: u8[], data: u8[], closed: boolean }
struct SStep { server: Srv, output: u8[], data: u8[] }
struct Failure { code: i32, alert: u8[] }

@noinline
function srv_read(s: Srv, rec: [u8]): Result[SStep, i32] {
  if (rec.len() == 0) {
    return Err(1);
  }
  return Ok(SStep { server: Srv { ...s, seq: s.seq + 1u64 }, output: [], data: [rec[0]] });
}

function record(s: Session, rec: [u8]): Result[Step, Failure] {
  match (srv_read(s.server, rec)) {
    Ok(st) => {
      return Ok(Step { session: Session { ...s, server: st.server }, output: st.output, data: st.data, closed: false });
    },
    Err(e) => {
      return Err(Failure { code: e, alert: [2u8] });
    }
  }
  return Err(Failure { code: 9, alert: [] });
}

@noinline
function rd(s: Session, buf: [u8]): Result[Step, Failure] {
  let session = s;
  let data: u8[] = [];
  let closed = false;
  let at = 0;
  while (!closed && buf.len() - at >= 2) {
    let next = record(session, buf[at:at + 2])?;
    session = next.session;
    data = next.data;
    closed = next.closed;
    at += 2;
  }
  return Ok(Step { session: Session { ...session, held: [] }, output: [], data: data, closed: closed });
}

function main(): i32 {
  let sess = Session { server: Srv { key: [1u8], seq: 0u64 }, held: [] };
  let before = __heap_alloc_count();
  let n = 0;
  for r in 0..1000 {
    let raw: u8[] = [7u8, 0u8];
    match (rd(sess, raw)) {
      Ok(step) => {
        sess = step.session;
        n += step.data.len();
      },
      Err(_) => {}
    }
  }
  let allocs = __heap_alloc_count() - before;
  if (n != 1000 || sess.server.seq != 1000u64) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (allocs > 5100 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostThreadedBranchSplitsItsRecord(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "threaded_split.fern")
	if err := os.WriteFile(src, []byte(threadedSplitProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a value came back wrong, 2: each call boxed its Step, 99: a count went under)", tg.target, got)
		}
	}
}
