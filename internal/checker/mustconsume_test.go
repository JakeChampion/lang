package checker

import (
	"strings"
	"testing"
)

// E067 — `@must_consume` obligation checking (docs/MUST-CONSUME.md).
// These pin both directions per shape: the consuming forms pass, the
// leaking forms produce E067 with the expected message flavour.

func wantE067(t *testing.T, name, src, msgFragment string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E067, got none", name)
	}
	if !strings.Contains(err.Error(), msgFragment) {
		t.Errorf("%s: expected message containing %q, got: %v", name, msgFragment, err)
	}
}

// sink consumes by passing the obligation back out (`return t`) —
// its own caller-side discard is the documented discarded-result
// hole. `own` params only accept fresh constructions / other own
// params (the owned-argument rule), so the own-sink shape is tested
// separately with a fresh construction.
const ticketDecl = `@must_consume
struct Ticket { id: i32 }
function sink(t: Ticket): Ticket { return t; }
`

const pendingDecl = `@must_consume
enum Pending { Reply(string), Close }
`

func TestMustConsumeCleanShapes(t *testing.T) {
	cases := []struct{ name, src string }{
		{"consumed_on_both_arms", ticketDecl + `function f(n: i32): void {
    var t: Ticket = Ticket { id: 1 };
    if (n > 0) { sink(t); } else { sink(t); }
}`},
		{"returned", ticketDecl + `function f(): Ticket {
    var t: Ticket = Ticket { id: 1 };
    return t;
}`},
		{"transferred_to_local", ticketDecl + `function f(): Ticket {
    var t: Ticket = Ticket { id: 1 };
    var u: Ticket = t;
    return u;
}`},
		{"own_param_is_the_sink", ticketDecl + `function take(own t: Ticket): void {
    print("this function owns and discharges t");
}
function f(): void {
    take(Ticket { id: 9 });
}`},
		{"non_own_param_passed_on", ticketDecl + `function f(t: Ticket): void {
    sink(t);
}`},
		{"match_consumes_enum", pendingDecl + `function f(p: Pending): i32 {
    match (p) {
        Reply(s) => { print(s); },
        Close => { print("closed"); }
    }
    return 0;
}`},
		{"consumed_before_early_return", ticketDecl + `function f(n: i32): i32 {
    var t: Ticket = Ticket { id: 1 };
    sink(t);
    if (n > 5) { return 1; }
    return 0;
}`},
		{"field_read_is_neutral", ticketDecl + `function f(): i32 {
    var t: Ticket = Ticket { id: 7 };
    var n: i32 = t.id;
    sink(t);
    return n;
}`},
		{"stored_into_marked_struct", ticketDecl + `@must_consume
struct Envelope { inner: Ticket }
function open_env(e: Envelope): Envelope { return e; }
function f(): void {
    var t: Ticket = Ticket { id: 1 };
    var e: Envelope = Envelope { inner: t };
    open_env(e);
}`},
		{"unmarked_types_unaffected", `struct Plain { id: i32 }
function f(): void {
    var p: Plain = Plain { id: 1 };
}
`},
	}
	for _, c := range cases {
		wantNoErr(t, c.name, c.src+"\nfunction main(): i32 { return 0; }")
	}
}

func TestMustConsumeLeaks(t *testing.T) {
	scopeMsg := "may go out of scope without being consumed"
	cases := []struct{ name, src, frag string }{
		{"plain_leak", ticketDecl + `function f(): void {
    var t: Ticket = Ticket { id: 1 };
}`, scopeMsg},
		{"one_arm_leaks", ticketDecl + `function f(n: i32): void {
    var t: Ticket = Ticket { id: 1 };
    if (n > 0) { sink(t); }
}`, scopeMsg},
		{"early_return_leaks", ticketDecl + `function f(n: i32): i32 {
    var t: Ticket = Ticket { id: 1 };
    if (n > 5) { return 1; }
    sink(t);
    return 0;
}`, scopeMsg},
		{"non_own_param_dropped", ticketDecl + `function f(t: Ticket): void {
    print("ignored");
}`, scopeMsg},
		{"enum_leak", pendingDecl + `function f(): void {
    var p: Pending = Close;
}`, scopeMsg},
		{"loop_consume_does_not_discharge", ticketDecl + `function f(n: i32): void {
    var t: Ticket = Ticket { id: 1 };
    while (n > 0) {
        sink(t);
        n = n - 1;
    }
}`, scopeMsg},
		{"laundered_into_array", ticketDecl + `function f(): void {
    var t: Ticket = Ticket { id: 1 };
    var arr: Ticket[] = [t];
}`, "stored into an array literal"},
		{"laundered_into_unmarked_struct", ticketDecl + `struct Box { inner: Ticket }
function f(): void {
    var t: Ticket = Ticket { id: 1 };
    var b: Box = Box { inner: t };
}`, "stored into unmarked struct"},
		{"laundered_into_tuple", ticketDecl + `function f(): (Ticket, i32) {
    var t: Ticket = Ticket { id: 1 };
    return (t, 3);
}`, "stored into a tuple"},
		{"captured_by_closure", ticketDecl + `function f(): void {
    var t: Ticket = Ticket { id: 1 };
    var g = () => t.id;
    print("captured");
}`, "captured by a closure"},
		{"overwrite_unconsumed", pendingDecl + `function f(): void {
    var p: Pending = Close;
    p = Reply("again");
    match (p) { Reply(s) => {}, Close => {} }
}`, "overwriting"},
	}
	for _, c := range cases {
		wantE067(t, c.name, c.src+"\nfunction main(): i32 { return 0; }", c.frag)
	}
}
