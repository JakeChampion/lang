package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A caller-owned string element handed to a callee that keeps it ---------
//
// `var nm: string = names[i]` inside a function borrowing `names` reads an
// element the CALLER's deep free releases, and `out.bind(nm, i)` hands it to
// a parameter that is stored (`s.names.append(name)`), which is neither
// borrowable nor counted. A string parameter at such a position takes over
// the argument's reference, and the element had none to give: the caller
// then freed it under the scope that stored it. The checker's own
// `for (k, v) in m` binding is this shape, and every self-host-built checker
// died in Scope.bind once __fern_arr_inc_elems walked the freed element
// (#9023). The handoff now retains the element
// (retain_caller_elem_handoff), the store's second owner.
//
// The exit is pinned against the interpreter under the sanitizer, which stays
// silent: a use-after-free is a `fern-sanitizer:` line and exit 124. A leak
// line is not a finding here: the holder scope is never released by this
// program, and its elements now correctly belong to it.

const strElemHandoffSrc = `struct Sc { names: string[], types: i32[] }
struct FB { scope: Sc, valid: boolean }

function (s: Sc) bind(name: string, t: i32): Sc {
  var ns: string[] = s.names.append(name);
  var ts: i32[] = s.types.append(t);
  return Sc { names: ns, types: ts };
}

function (s: Sc) enter_loop(): Sc {
  return Sc { names: s.names, types: s.types };
}

function split(enc: string): string[] {
  var out: string[] = [];
  var start: i32 = 0;
  var i: i32 = 0;
  while (i < enc.len()) {
    if (enc[i] == 44) {
      out = out.append(slice_unchecked(enc, start, i) + "");
      start = i + 1;
    }
    i = i + 1;
  }
  out = out.append(slice_unchecked(enc, start, enc.len()) + "");
  return out;
}

function bind_names(out: Sc, names: string[]): Sc {
  var i: i32 = 0;
  while (i < names.len()) {
    var nm: string = names[i];
    if (nm.len() > 0) {
      out = out.bind(nm, i);
    }
    i = i + 1;
  }
  return out;
}

function for_binding(enc: string, s: Sc): FB {
  var body = s.enter_loop();
  var names = split(enc);
  return FB { scope: bind_names(body, names), valid: names.len() == 2 };
}

function main(): i32 {
  var s: Sc = Sc { names: [], types: [] };
  var r: Sc = for_binding("k,v", s).scope;
  r = r.bind("text", 3);
  var total: i32 = 0;
  var j: i32 = 0;
  while (j < r.names.len()) {
    total = total + r.names[j].len() + r.types[j];
    j = j + 1;
  }
  return total;
}
`

// The x86-64 leg pins the retain in the handing function and runs the result
// under the sanitizer against the interpreter's exit.
func TestSelfHostStrElemHandoffX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, strElemHandoffSrc)
	asm := runCaptureEnv(t, runner, driverBin, []byte(strElemHandoffSrc),
		[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"}, "-ir")
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	if n := strings.Count(emittedFn(t, string(asm), "bind_names"), "call __fn___fern_rc_inc"); n != 1 {
		t.Errorf("bind_names carries %d retain(s), want exactly one: the element handed to bind", n)
	}
	bin := buildBin(t, gcc, dir, "str_elem_handoff", string(asm))
	stderr, code := runCaptureStderrExit(t, runner, bin)
	if code != want {
		t.Fatalf("exited %d under the sanitizer, want %d (interp oracle; 124 = fatal sanitizer check, 99 = rc underflow)", code, want)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
			t.Errorf("%s", line)
		}
	}
}
