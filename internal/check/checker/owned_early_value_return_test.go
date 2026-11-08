package checker

import "testing"

const ownedEarlyValuePrefix = `struct State { values:i32[] }
function take(own value:State):i32 { return value.values.len(); }
`

func TestOwnedEarlyReturnedValues(t *testing.T) {
	for _, body := range []string{
		`if(early) {return if(flag) {take(value)} else {take(value)};} return take(value);`,
		`if(early) {return match(flag) {true=>take(value),false=>take(value)};} return take(value);`,
		`if(early) {return if(flag) {if(flag) {take(value)} else {take(value)}} else {take(value)};} return take(value);`,
		`for i in [0,1] {if(early) {return if(flag) {take(value)} else {take(value)};}} return take(value);`,
	} {
		wantOK(t, body, ownedEarlyValuePrefix+`function work(early:boolean,flag:boolean):i32 {let value=State {values:[1]};`+body+`}`)
	}
}
func TestOwnedEarlyReturnedValuesKeepLaterReads(t *testing.T) {
	for _, body := range []string{
		`if(early) {return (if(flag) {take(value)} else {0}) + value.values.len();} return take(value);`,
		`if(early) {return if(flag) {take(value)+take(value)} else {0};} return take(value);`,
		`let n=if(flag) {take(value)} else {0}; return n+value.values.len();`,
		`for i in [0,1] {let n=if(flag) {take(value)} else {0};} return take(value);`,
		`let later=()=>value.values.len(); if(early) {return if(flag) {take(value)} else {0};} return later();`,
	} {
		source := ownedEarlyValuePrefix + `function work(early:boolean,flag:boolean):i32 {let value=State {values:[1]};` + body + `}`
		if err := checkSource(t, source); !hasCode(err, "E050") && !hasCode(err, "E051") {
			t.Fatalf("want ownership refusal for %s: %v", body, err)
		}
	}
}
