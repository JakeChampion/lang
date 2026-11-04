package parser

import "testing"

func TestSendParamIsOwnAndMarked(t *testing.T) {
	prog, err := Parse(`function spawn(send body: () => i32, n: i32): i32 { return body() + n; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ps := prog.Funcs[0].Params
	if !ps[0].Own || !ps[0].Send {
		t.Fatalf("send param: own=%v send=%v, want both true", ps[0].Own, ps[0].Send)
	}
	if ps[1].Own || ps[1].Send {
		t.Fatalf("plain param: own=%v send=%v, want both false", ps[1].Own, ps[1].Send)
	}
}

func TestSendAsAParameterNameStaysAName(t *testing.T) {
	prog, err := Parse(`function f(send: i32): i32 { return send; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := prog.Funcs[0].Params[0]
	if p.Name != "send" || p.Own || p.Send {
		t.Fatalf("param = %+v, want a plain parameter named send", p)
	}
}
