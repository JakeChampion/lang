package ir

import "testing"

// A match whose arm reassigns the scrutinee local holds its own count on the
// box for the match's duration: the retain after the scrutinee is parked,
// and the box's release at the match's end. Without it the assignment drops
// the box while the arm's binding still reads its payload (#11349's
// reviewer found the enum drop freeing a closure under a live binding).
func TestMatchHoldsScrutineeAnArmReassigns(t *testing.T) {
	const reassigns = `enum Job { Run((i32) => i32), Idle }
function make(k: i32): Job {
  return Run((x: i32) => x + k);
}
function main(): i32 {
  let e: Job = make(40);
  let out: i32 = 0;
  match (e) {
    Run(f) => {
      e = Idle;
      out = f(2);
    },
    Idle => {}
  }
  return out;
}`
	const keeps = `enum Job { Run((i32) => i32), Idle }
function make(k: i32): Job {
  return Run((x: i32) => x + k);
}
function main(): i32 {
  let e: Job = make(40);
  let out: i32 = 0;
  match (e) {
    Run(f) => {
      out = f(2);
    },
    Idle => {}
  }
  return out;
}`
	// The arm reads f only in the value it assigns, which is evaluated
	// before the old box drops.
	const readsInValue = `enum Job { Run((i32) => i32), Idle }
function make(k: i32): Job {
  return Run((x: i32) => x + k);
}
function main(): i32 {
  let e: Job = make(40);
  let out: i32 = 0;
  match (e) {
    Run(f) => {
      e = make(f(2));
    },
    Idle => {}
  }
  return out;
}`
	// The assignment is the arm's last statement.
	const assignsLast = `enum Job { Run((i32) => i32), Idle }
function make(k: i32): Job {
  return Run((x: i32) => x + k);
}
function main(): i32 {
  let e: Job = make(40);
  let out: i32 = 0;
  match (e) {
    Run(f) => {
      out = f(2);
      e = Idle;
    },
    Idle => {}
  }
  return out;
}`
	// The assignment sits in a loop whose next pass reads f before it.
	const loopReads = `enum Job { Run((i32) => i32), Idle }
function make(k: i32): Job {
  return Run((x: i32) => x + k);
}
function main(): i32 {
  let e: Job = make(40);
  let out: i32 = 0;
  match (e) {
    Run(f) => {
      let i: i32 = 0;
      while (i < 2) {
        out = out + f(i);
        e = make(i);
        i = i + 1;
      }
    },
    Idle => {}
  }
  return out;
}`
	for _, c := range []struct {
		name  string
		src   string
		holds bool
	}{
		{"reassigns then reads", reassigns, true},
		{"never reassigns", keeps, false},
		{"reads only in the assigned value", readsInValue, false},
		{"assigns last", assignsLast, false},
		{"assigns in a loop that reads", loopReads, true},
	} {
		if got := matchRetainsScrutinee(lowerSourceWith(t, c.src, 8)); got != c.holds {
			t.Errorf("%s: main retains the parked scrutinee = %v, want %v", c.name, got, c.holds)
		}
	}
}

// matchRetainsScrutinee reports the shape emitMatchScrutineeRetain emits in
// main: the parked scrutinee stored, loaded back, retained and dropped.
func matchRetainsScrutinee(p *Program) bool {
	for _, fn := range p.Funcs {
		if fn.Name != "main" {
			continue
		}
		for i := 0; i+3 < len(fn.Ops); i++ {
			if fn.Ops[i].Kind == OpStoreLocal && fn.Ops[i+1].Kind == OpLoadLocal && fn.Ops[i].I32 == fn.Ops[i+1].I32 &&
				fn.Ops[i+2].Kind == OpRcInc && fn.Ops[i+3].Kind == OpDrop {
				return true
			}
		}
	}
	return false
}
