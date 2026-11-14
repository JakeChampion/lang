package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A method that stores only its receiver does not store its first argument.
// The store analysis of #15741 read a call through a field as a method or as
// a function of a type, so it marked the text of Finder.twice as stored, and
// measure copied the view on each call (#15820). measure_plain makes the
// same calls through plain functions, so the two loops allocate the same.
// Saver.remember stores its text, and Store.keep stores its first
// parameter, so the views that reach them through a method are still
// copied. A call of a closure can store each argument, so via_param and
// via_field copy their views.
const methodCallStoreProgram = `struct Finder { n: i32, seen: Cell[i32] }

function first(f: Finder, x: i32): i32 {
  return f.n;
}

// A method that stores a value made from its receiver in a cell of the
// receiver, but not its text.
@noinline function (f: Finder) find(text: string): i32 {
  f.seen.set(first(f, f.seen.get()));
  return text.len();
}

@noinline function (f: Finder) twice(text: string): i32 {
  return f.find(text) * 2;
}

@noinline function measure(f: Finder, src: string, lo: i32, hi: i32): i32 {
  return f.twice(slice_unchecked(src, lo, hi));
}

// The same calls as plain functions, which the analysis always read right.
@noinline function find_plain(f: Finder, text: string): i32 {
  f.seen.set(first(f, f.seen.get()));
  return text.len();
}

@noinline function twice_plain(f: Finder, text: string): i32 {
  return find_plain(f, text) * 2;
}

@noinline function measure_plain(f: Finder, src: string, lo: i32, hi: i32): i32 {
  return twice_plain(f, slice_unchecked(src, lo, hi));
}

// A method that stores its text, so a view that it gets is copied.
struct Saver { kept: Cell[string] }

@noinline function (v: Saver) remember(text: string): i32 {
  v.kept.set(text);
  return 0;
}

@noinline function (v: Saver) remember_via(text: string): i32 {
  return v.remember(text) + 1;
}

// A function of a type that stores its first parameter.
trait Keep {
  function keep(text: string, into: Cell[string]): i32;
}

struct Store { n: i32 }

impl Keep for Store {
  function keep(text: string, into: Cell[string]): i32 {
    into.set(text);
    return 0;
  }
}

@noinline function fresh(unit: string): string {
  let out = "";
  for i in 0..12 {
    out = out + unit;
  }
  return out;
}

@noinline function kept_by_method(v: Saver): i32 {
  let src = fresh("ef");
  return v.remember_via(slice_unchecked(src, 2, 10));
}

// A method that gives its text to the function of a type that stores it.
@noinline function (v: Saver) keep_by_type(text: string, c: Cell[string]): i32 {
  return Store.keep(text, c);
}

@noinline function kept_by_type(v: Saver, c: Cell[string]): i32 {
  let src = fresh("gh");
  return v.keep_by_type(slice_unchecked(src, 2, 10), c);
}

// A closure that stores its argument, called through a parameter and
// through a field.
struct Holder { cb: (string) => i32 }

@noinline function call_it(cb: (string) => i32, s: string): i32 {
  return cb(s);
}

@noinline function call_field(h: Holder, s: string): i32 {
  return h.cb(s);
}

@noinline function via_param(cb: (string) => i32): i32 {
  let src = fresh("mn");
  return call_it(cb, slice_unchecked(src, 2, 10));
}

@noinline function via_field(h: Holder): i32 {
  let src = fresh("op");
  return call_field(h, slice_unchecked(src, 2, 10));
}

@noinline function churn(): i32 {
  let total = 0;
  for i in 0..256 {
    let junk = fresh("zq");
    total += junk.len();
  }
  return total;
}

function main(): i32 {
  let v = Saver { kept: cell_new("") };
  if (kept_by_method(v) != 1) { return 21; }
  if (churn() != 6144) { return 20; }
  if (v.kept.get() != "efefefef") { return 22; }
  let c = cell_new("");
  if (kept_by_type(v, c) != 0) { return 23; }
  if (churn() != 6144) { return 20; }
  if (c.get() != "ghghghgh") { return 24; }
  let d = cell_new("");
  let saver = (s: string): i32 => {
    d.set(s);
    return 0;
  };
  if (via_param(saver) != 0) { return 29; }
  if (churn() != 6144) { return 20; }
  if (d.get() != "mnmnmnmn") { return 30; }
  if (via_field(Holder { cb: saver }) != 0) { return 31; }
  if (churn() != 6144) { return 20; }
  if (d.get() != "opopopop") { return 32; }
  let f = Finder { n: 3, seen: cell_new(0) };
  let src = fresh("x");
  let n = measure(f, src, 0, 4) + measure_plain(f, src, 0, 4);
  let before: i64 = __heap_alloc_count();
  for k in 0..10 {
    n += measure(f, src, 0, 2 + k % 8);
  }
  let by_method = __heap_alloc_count() - before;
  before = __heap_alloc_count();
  for k in 0..10 {
    n += measure_plain(f, src, 0, 2 + k % 8);
  }
  if (by_method != __heap_alloc_count() - before) { return 25; }
  if (n != 16 + 4 * (2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 2 + 3)) { return 26; }
  if (f.seen.get() != 3) { return 27; }
  if (__rc_underflow_count() != 0) { return 28; }
  print("method calls lend views");
  return 0;
}
`

// The body of the function `name` in the assembly or the text format of
// target, and the name of the runtime function that copies a string there.
func methodCallBody(asm, target, name string) (string, string) {
	if target == "wasm32-wasi" {
		at := strings.Index(asm, "(func $"+name+" ")
		if at < 0 {
			return "", ""
		}
		body := asm[at+1:]
		if end := strings.Index(body, "\n  (func "); end >= 0 {
			body = body[:end]
		}
		return body, "$__fern_strcat"
	}
	return asmFunction(asm, "__fn_"+name+":"), "__fern_str_concat"
}

func TestSelfHostMethodCallStoreReach(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(methodCallStoreProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			raw, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			asm := string(raw)
			copies := map[string]bool{"measure": false, "kept_by_method": true, "kept_by_type": true, "via_param": true, "via_field": true}
			for name, want := range copies {
				body, copier := methodCallBody(asm, target, name)
				if body == "" {
					t.Fatalf("no function %s in the output for %s", name, target)
				}
				if got := strings.Contains(body, copier); got != want {
					t.Errorf("%s copies its view: %v, want %v", name, got, want)
				}
			}
			for _, sanitize := range []bool{false, true} {
				if got, _ := runLentView(t, cli, methodCallStoreProgram, target, sanitize); got != "method calls lend views\n" {
					t.Fatalf("sanitize=%v: got %q", sanitize, got)
				}
			}
		})
	}
}
