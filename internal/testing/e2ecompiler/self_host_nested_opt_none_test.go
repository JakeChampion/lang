package e2ecompiler

import "testing"

// --- A `Some(None)` payload keeps the binding's declared type (#7217) -------
//
//	let o: Option[Option[i32]] = Some(None);
//
// The `Some(inner)` arm binds the inner Option, so a nested `match (inner)`
// must see an Option scrutinee, not the `Option[i32]` a bare `None` argument
// would suggest. The neighbours pin the other payload shapes (nested Some,
// nested Result, a `None` routed through a local, an unannotated binding, an
// array payload that escapes, a string payload, a reassigned local).
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend — and every row balances at live_bytes 0.
type nestedOptNoneCase struct {
	name      string
	src       string
	want      int
	wantFrees int // when non-zero, assert an exact free count
}

const nestedOptNoneMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func nestedOptNoneCases() []nestedOptNoneCase {
	return []nestedOptNoneCase{
		{
			// THE REPRO: a nested match on the `Some(None)` payload.
			name: "nested_none",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(None);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19,
		},
		{
			// The nested-Some neighbour.
			name: "nested_some",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 63,
		},
		{
			// A nested RESULT payload.
			name: "nested_result",
			src: `function round(i: i32): i32 {
    let o: Option[Result[i32, i32]] = Some(Ok(i));
    match (o) {
        Some(inner) => { match (inner) { Ok(v) => { return v; }, Err(e) => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 63,
		},
		{
			// The same `None` routed through an annotated local.
			name: "via_local",
			src: `function round(i: i32): i32 {
    let inner0: Option[i32] = None;
    let o: Option[Option[i32]] = Some(inner0);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19,
		},
		{
			// No annotation: the payload's type comes from the construction
			// alone, which the checker accepts only while the payload goes
			// unread.
			name: "unannotated_none",
			src: `function round(i: i32): i32 {
    let o = Some(None);
    match (o) { Some(inner) => { return 7; }, None => { return 2; } }
    return 0;
}` + nestedOptNoneMain,
			want: 72,
		},
		{
			// The annotated single-level match.
			name: "single_level",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(None);
    match (o) { Some(inner) => { return 7; }, None => { return 2; } }
    return 0;
}` + nestedOptNoneMain,
			want: 72,
		},
		{
			// A scalar-array payload that escapes the match into an outer
			// local. Two boxes a round (the array and the Some), both freed:
			// pinned exactly, so a dropped reclaim or an over-release shows.
			name: "array_payload_escapes",
			src: `function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { held = a; acc = a[0]; }, None => {} }
    }
    return acc + held[1];
}` + nestedOptNoneMain,
			want: 77, wantFrees: 400,
		},
		{
			// A STRING payload.
			name: "nested_string",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 19,
		},
		{
			// A REASSIGNED Option local.
			name: "reassigned",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(None);
    if (i % 2 == 0) { o = Some(Some(i)); }
    match (o) {
        Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } },
        None => { return 2; }
    }
    return 0;
}` + nestedOptNoneMain,
			want: 74,
		},
	}
}

// TestSelfHostNestedOptNoneX86_64 — a `Some(None)` payload keeps the binding's
// declared Option type, so the nested `match` lowers, and every row balances.
func TestSelfHostNestedOptNoneX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	for _, tc := range nestedOptNoneCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "nestoptnone", asm))
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.wantFrees != 0 && frees != int64(tc.wantFrees) {
				t.Errorf("%s: %s — want exactly %d frees", tc.name, summary, tc.wantFrees)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
		})
	}
}

// TestSelfHostNestedOptNoneWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostNestedOptNoneWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range nestedOptNoneCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("nested Some(None) wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostNestedOptNoneIRArm64 — the arm64 sibling under qemu.
func TestSelfHostNestedOptNoneIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range nestedOptNoneCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("nested Some(None) arm64 %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
