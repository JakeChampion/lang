package x86_64_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// TestInstRoundTrip is the gate on the structured entry point (#7993): for
// every instruction line the form inventory can generate, the Inst that
// ParseInst reads renders (String) to a line that parses back to the same
// Inst, and a program fed through NewProgram/Label/Directive/Inst encodes
// to the bytes the text path gives. The renderer is what a code generator
// building Inst values writes out as text, so it has to be the parser's
// inverse over the whole surface.
func TestInstRoundTrip(t *testing.T) {
	seed := fuzzSeed(t)
	n := fuzzCaseCount()
	for _, f := range x86Forms() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			src := strings.Join(formUnits(f, formRand(seed, f.name), n), "")
			wantText, _, err := x86_64.AssembleProgram(src, elf.TextVAddr)
			if err != nil {
				t.Fatalf("text path: %v", err)
			}
			structured := x86_64.NewProgram()
			var rendered strings.Builder
			for _, line := range strings.Split(src, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if strings.HasSuffix(line, ":") {
					structured.Label(strings.TrimSuffix(line, ":"))
					rendered.WriteString(line + "\n")
					continue
				}
				if strings.HasPrefix(line, ".") {
					if err := structured.Directive(line); err != nil {
						t.Fatalf("directive %q: %v", line, err)
					}
					rendered.WriteString(line + "\n")
					continue
				}
				in, err := x86_64.ParseInst(line)
				if err != nil {
					t.Fatalf("ParseInst(%q): %v", line, err)
				}
				s := in.String()
				again, err := x86_64.ParseInst(s)
				if err != nil {
					t.Fatalf("ParseInst(String(%q) = %q): %v", line, s, err)
				}
				if !reflect.DeepEqual(in, again) {
					t.Fatalf("String is not the parser's inverse for %q: rendered %q, reparsed %+v, want %+v", line, s, again, in)
				}
				if err := structured.Inst(in); err != nil {
					t.Fatalf("Inst(%q): %v", line, err)
				}
				rendered.WriteString(s + "\n")
			}
			gotStruct, _, err := structured.BytesProgram(elf.TextVAddr)
			if err != nil {
				t.Fatalf("structured path: %v", err)
			}
			if string(gotStruct) != string(wantText) {
				t.Fatalf("structured bytes differ from the text path (seed %d)\n text:   % x\n struct: % x", seed, wantText, gotStruct)
			}
			gotRendered, _, err := x86_64.AssembleProgram(rendered.String(), elf.TextVAddr)
			if err != nil {
				t.Fatalf("rendered text: %v\n%s", err, rendered.String())
			}
			if string(gotRendered) != string(wantText) {
				t.Fatalf("re-rendered program encodes differently (seed %d)", seed)
			}
		})
	}
}

// TestInstConstructors pins the constructors against the parser on the
// operand shapes a code generator builds directly.
func TestInstConstructors(t *testing.T) {
	cases := []struct {
		line string
		inst x86_64.Inst
	}{
		{"mov rax, rcx", x86_64.Inst{Mnem: "mov", Ops: []x86_64.Operand{x86_64.Reg(0, 64), x86_64.Reg(1, 64)}}},
		{"mov r9d, 5", x86_64.Inst{Mnem: "mov", Ops: []x86_64.Operand{x86_64.Reg(9, 32), x86_64.Imm(5)}}},
		{"mov al, ah", x86_64.Inst{Mnem: "mov", Ops: []x86_64.Operand{x86_64.Reg(0, 8), x86_64.HighByte(4)}}},
		{"mov qword ptr [rbp - 8], rdi", x86_64.Inst{Mnem: "mov", Ops: []x86_64.Operand{x86_64.Mem(5, -1, 1, -8, 64), x86_64.Reg(7, 64)}}},
		{"lea rax, [rsi + rcx*8 + 16]", x86_64.Inst{Mnem: "lea", Ops: []x86_64.Operand{x86_64.Reg(0, 64), x86_64.Mem(6, 1, 8, 16, 0)}}},
		{"lea rax, [rip + sym]", x86_64.Inst{Mnem: "lea", Ops: []x86_64.Operand{x86_64.Reg(0, 64), x86_64.RIPRel("sym", 0, 0)}}},
		{"mov dword ptr [rip + counter + 4], 1", x86_64.Inst{Mnem: "mov", Ops: []x86_64.Operand{x86_64.RIPRel("counter", 4, 32), x86_64.Imm(1)}}},
		{"movsd xmm0, xmm1", x86_64.Inst{Mnem: "movsd", Ops: []x86_64.Operand{x86_64.Xmm(0), x86_64.Xmm(1)}}},
		{"jmp .L1", x86_64.Inst{Mnem: "jmp", Ops: []x86_64.Operand{x86_64.Sym(".L1")}}},
		{"rep movsb", x86_64.Inst{Mnem: "movsb", Prefix: x86_64.PrefixRep}},
		{"lock xadd qword ptr [rdi], rax", x86_64.Inst{Mnem: "xadd", Prefix: x86_64.PrefixLock, Ops: []x86_64.Operand{x86_64.Mem(7, -1, 1, 0, 64), x86_64.Reg(0, 64)}}},
		{"ret", x86_64.Inst{Mnem: "ret"}},
	}
	for _, c := range cases {
		got, err := x86_64.ParseInst(c.line)
		if err != nil {
			t.Fatalf("ParseInst(%q): %v", c.line, err)
		}
		if !reflect.DeepEqual(got, c.inst) {
			t.Errorf("ParseInst(%q) = %+v, want %+v", c.line, got, c.inst)
		}
		if s := c.inst.String(); s != c.line {
			t.Errorf("String() = %q, want %q", s, c.line)
		}
	}
	if r, ok := x86_64.RegNamed("r12d"); !ok || r != x86_64.Reg(12, 32) {
		t.Errorf("RegNamed(r12d) = %+v, %v", r, ok)
	}
	if _, ok := x86_64.RegNamed("42"); ok {
		t.Error("RegNamed accepted a non-register")
	}
}

// TestInstRefusals: the structured entry refuses what the text one does.
func TestInstRefusals(t *testing.T) {
	a := x86_64.NewProgram()
	if err := a.Inst(x86_64.Inst{Mnem: "add", Prefix: x86_64.PrefixRep, Ops: []x86_64.Operand{x86_64.Reg(0, 64), x86_64.Reg(1, 64)}}); err == nil {
		t.Error("rep add accepted")
	}
	if err := a.Inst(x86_64.Inst{Mnem: "mov", Prefix: x86_64.PrefixLock, Ops: []x86_64.Operand{x86_64.Reg(0, 64), x86_64.Reg(1, 64)}}); err == nil {
		t.Error("lock mov accepted")
	}
	if err := a.Inst(x86_64.Inst{Mnem: "frobnicate"}); err == nil || !strings.Contains(err.Error(), "unsupported instruction") {
		t.Errorf("unknown mnemonic: %v", err)
	}
	if err := a.Directive(".section .rodata"); err != nil {
		t.Fatal(err)
	}
	if err := a.Inst(x86_64.Inst{Mnem: "ret"}); err == nil || !strings.Contains(err.Error(), "outside .text") {
		t.Errorf("instruction in .rodata: %v", err)
	}
}
