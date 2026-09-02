package e2eharness

// The structured entry point of the x86-64 assembler (#7993) against the
// text one, over the asm the Go backend emits for a real program: the same
// bytes whichever way the program arrives, and the renderer is the parser's
// inverse over everything the code generator writes. The benchmark is the
// encode floor the text interface sits on top of.
//
//	go test -run '^$' -bench BenchmarkAssembleX86_64Structured ./internal/e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/x86_64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

// x86Item is one line of a program after the text has been read once:
// a label, a directive line, or an instruction value.
type x86Item struct {
	label     string
	directive string
	inst      nativex86.Inst
	isInst    bool
}

// x86Items reads the program text into items, the way a code generator
// would hand it over directly.
func x86Items(t testing.TB, src string) []x86Item {
	t.Helper()
	var items []x86Item
	for _, raw := range strings.Split(src, "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		for {
			i := strings.IndexByte(line, ':')
			if i <= 0 || strings.ContainsAny(line[:i], " \t[") {
				break
			}
			items = append(items, x86Item{label: line[:i]})
			line = strings.TrimSpace(line[i+1:])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			items = append(items, x86Item{directive: line})
			continue
		}
		in, err := nativex86.ParseInst(line)
		if err != nil {
			t.Fatalf("ParseInst(%q): %v", raw, err)
		}
		items = append(items, x86Item{inst: in, isInst: true})
	}
	return items
}

func x86Replay(items []x86Item) (*nativex86.Assembler, error) {
	a := nativex86.NewProgram()
	for i := range items {
		it := &items[i]
		var err error
		switch {
		case it.isInst:
			err = a.Inst(it.inst)
		case it.directive != "":
			err = a.Directive(it.directive)
		default:
			a.Label(it.label)
		}
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}

func x86AssembleWX(a *nativex86.Assembler) (text, rodata []byte, err error) {
	n, err := a.TextLen()
	if err != nil {
		return nil, nil, err
	}
	tv, dv := nativeelf.SegmentAddrsWXX86(n)
	return a.BytesProgramWX(tv, dv)
}

func x86MediumAsm(t testing.TB) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(p, []byte(asmBenchMedium), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := asmBenchEmit(p, x86_64.Emit)
	if err != nil {
		t.Fatal(err)
	}
	return asm
}

func TestX86StructuredMatchesText(t *testing.T) {
	src := x86MediumAsm(t)
	wantText, wantData, err := nativex86.AssembleProgramWX(src, nativeelf.SegmentAddrsWXX86)
	if err != nil {
		t.Fatal(err)
	}
	items := x86Items(t, src)
	a, err := x86Replay(items)
	if err != nil {
		t.Fatal(err)
	}
	gotText, gotData, err := x86AssembleWX(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotText) != string(wantText) || string(gotData) != string(wantData) {
		t.Fatal("structured program encodes differently from the text it was read from")
	}

	// The renderer as the generator's text output: every instruction goes
	// out through Inst.String and comes back the same.
	var rendered strings.Builder
	insts := 0
	for _, it := range items {
		switch {
		case it.isInst:
			rendered.WriteString(it.inst.String() + "\n")
			insts++
		case it.directive != "":
			rendered.WriteString(it.directive + "\n")
		default:
			rendered.WriteString(it.label + ":\n")
		}
	}
	if insts < 1000 {
		t.Fatalf("corpus too small to mean anything: %d instructions", insts)
	}
	againText, againData, err := nativex86.AssembleProgramWX(rendered.String(), nativeelf.SegmentAddrsWXX86)
	if err != nil {
		t.Fatalf("re-rendered program: %v", err)
	}
	if string(againText) != string(wantText) || string(againData) != string(wantData) {
		t.Fatal("re-rendered program encodes differently from the original text")
	}
}

// BenchmarkAssembleX86_64Structured assembles the medium corpus from
// already-built items, the cost the text front end adds on top being what
// BenchmarkAssembleX86_64/medium measures.
func BenchmarkAssembleX86_64Structured(b *testing.B) {
	src := asmBenchCorpus(b, asmBenchX86, "medium")
	items := x86Items(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a, err := x86Replay(items)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := x86AssembleWX(a); err != nil {
			b.Fatal(err)
		}
	}
}
