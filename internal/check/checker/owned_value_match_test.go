package checker

import "testing"

const ownedValueMatchPrefix = `struct State { values: i32[] }
struct Wrapper { state: State, selected: Option[i32] }
function consume(own state: State): i32 { return state.values.len(); }
function guard(own state: State): boolean { return state.values.len() > 0; }
`

func TestOwnedLocalInExclusiveValueMatchArms(t *testing.T) {
	for _, body := range []string{
		`let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => consume(state) };`,
		`let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => { if (tag > 1) { consume(state) } else { consume(state) } } };`,
		`let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => match (tag) { 1 => consume(state), _ => consume(state) } };`,
		`let state = State { values: [1] }; let n = match (tag) { 0 when tag > 1 => consume(state), _ => consume(state) }; return n;`,
		`let wrapper = Wrapper { state: State { values: [1] }, selected: None }; let Wrapper { state, selected } = wrapper; return match (selected) { None => consume(state), Some(value) => consume(state) };`,
	} {
		wantOK(t, body, ownedValueMatchPrefix+"function work(tag: i32): i32 { "+body+" }")
	}
}

func TestOwnedValueMatchRetainsLaterReads(t *testing.T) {
	for _, body := range []string{
		`let n = match (tag) { 0 => consume(state), _ => consume(state) }; return n + state.values.len();`,
		`return (match (tag) { 0 => consume(state), _ => consume(state) }) + state.values.len();`,
		`return match (tag) { 0 => consume(state) + consume(state), _ => 0 };`,
		`return match (tag) { 0 when guard(state) => 0, _ => consume(state) };`,
		`let later = () => state.values.len(); return match (tag) { 0 => consume(state), _ => consume(state) } + later();`,
		`let n = 0; for i in [0, 1] { n += match (tag) { 0 => consume(state), _ => consume(state) }; } return n;`,
	} {
		source := ownedValueMatchPrefix + "function work(tag: i32): i32 { let state = State { values: [1] }; " + body + " }"
		if err := checkSource(t, source); !hasCode(err, "E050") && !hasCode(err, "E051") {
			t.Fatalf("want ownership error: %s: %v", body, err)
		}
	}
	wantE051(t, "borrowed", ownedValueMatchPrefix+`function work(state: State, tag: i32): i32 { return match (tag) { 0 => consume(state), _ => consume(state) }; }`)
}
