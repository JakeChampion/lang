package checker

import "testing"

const freshTuplePrefix = `struct State { values: i32[] }
enum Event { Finished, Output(i32) }
function (own state: State) step_event(): (State, Event) { return (state, Event.Finished); }
function (own state: State) next_event(): (State, Option[i32]) { return (state, None); }
function make(): State { return State { values: [1] }; }
function (state: State) borrowed(): (State, i32) { return (state, 1); }
function (own state: State) step(): (State, i32) { return (state, 1); }
`

func TestFreshConditionalTupleLoop(t *testing.T) {
	for _, tail := range []string{
		`let (next, n) = state.step(); (next, n)`,
		`let (next, n) = state.step(); (next, 1)`,
		`let next = make(); (next, 1)`,
	} {
		wantOK(t, tail, freshTuplePrefix+`function work(flag: boolean): i32 {
 let state = make();
 for i in [0,1] { let (next,n)=if(flag) {state.step()} else { `+tail+` }; state=next; }
 return state.values.len(); }`)
	}
}
func TestFreshConditionalTupleMappedEvent(t *testing.T) {
	wantOK(t, "mapped event", freshTuplePrefix+`function work(flag:boolean):i32 {
 let state=make();
 for i in [0,1] {
  let(next,event)=if(flag) { state.step_event() } else {let(next,event)=state.next_event(); (next,match(event) {Some(value)=>Event.Output(value),None=>Event.Finished})};
  state=next;
 }
 return state.values.len(); }`)
}

func TestFreshConditionalTupleRejectsBorrowedPayloads(t *testing.T) {
	for _, tail := range []string{
		`(borrowed, 1)`,
		`let next=borrowed; (next,1)`,
		`let next=make(); let alias=next; (next,1)`,
		`let next=make(); let captured=()=>next.values.len(); (next,1)`,
		`let next=make(); next=borrowed; (next,1)`,
		`let (next,n)=(borrowed,1); (next,n)`, `let (next,n)=borrowed.borrowed(); (next,n)`,
	} {
		source := freshTuplePrefix + `function work(flag:boolean, borrowed:State):i32 {
 let state=make();
 for i in [0,1] { let(next,n)=if(flag) {state.step()} else { ` + tail + ` }; state=next; }
 return state.values.len(); }`
		if err := checkSource(t, source); !hasCode(err, "E051") && !hasCode(err, "E050") {
			t.Fatalf("want ownership refusal for %s: %v", tail, err)
		}
	}
}

func TestFreshConditionalTupleRejectsRepeatedPayload(t *testing.T) {
	source := freshTuplePrefix + `function (own pair:(State,State)) step_pair():((State,State),i32) {return(pair,1);}
 function work(flag:boolean):i32 { let pair=(make(),make()); for i in [0,1] {
 let(next,n)=if(flag) {pair.step_pair()} else {let(next,n)=pair.step_pair();let(a,b)=next;((a,a),n)};
 pair=next;
 } return 0; }`
	if err := checkSource(t, source); !hasCode(err, "E051") && !hasCode(err, "E050") {
		t.Fatalf("want repeated payload refusal: %v", err)
	}
}
