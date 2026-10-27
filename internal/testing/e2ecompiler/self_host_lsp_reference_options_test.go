package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const lspReferenceOptionProbe = `import "./lspquery";
import "./lsprefs";
import "./parser";
import "./lexer";
import "./util";
import "./drivers/rundriver";

function main():i32 {
 let source=rundriver.read_stdin("reference_probe");
 let parsed=parser.parse_module(lexer.tokenize(source));
 let av=args();
 let mod=if(av.len()>3){parser.Module{...parsed,funcs:parsed.funcs.with(0,parser.FuncDecl{...parsed.funcs[0],line:0,col:0})}}else{parsed};
 let p=lspquery.viewed(lspquery.program(mod,mod,[],[source]));
 let refs=lsprefs.references(p,util.digits_to_i32(av[1]),util.digits_to_i32(av[2]));
 print(refs.len());
 for r in refs {print(util.i32_to_string(r.line)+":"+util.i32_to_string(r.col)+":"+util.i32_to_string(r.len)+"|"+if(r.decl){"D"}else{"U"});}
 return 0;
}
`

func TestSelfHostLSPReferenceOptions(t *testing.T) {
	cases := []struct {
		name, source string
		args         []string
		want         string
	}{
		{name: "function-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"2", "28"}, want: "2\n1:10:3|D\n2:28:3|U\n"},
		{name: "parameter-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"1", "32"}, want: "2\n1:14:1|D\n1:32:1|U\n"},
		{name: "local-declaration", source: "function main():i32{let n=3;return n;}", args: []string{"1", "25"}, want: "2\n1:25:1|D\n1:36:1|U\n"},
		{name: "shadow", source: "function main():i32{let n=3;{let n=\"hi\";print(n);}return n;}", args: []string{"1", "47"}, want: "2\n1:34:1|D\n1:47:1|U\n"},
		{name: "field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"2", "43"}, want: "3\n1:10:1|D\n2:29:1|U\n2:43:1|U\n"},
		{name: "field-declaration", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"1", "10"}, want: "3\n1:10:1|D\n2:29:1|U\n2:43:1|U\n"},
		{name: "method", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"3", "43"}, want: "2\n2:14:3|D\n3:43:3|U\n"},
		{name: "builtin-string", source: "function main():i32{let s=\"abc\";return s.len();}", args: []string{"1", "42"}, want: "0\n"},
		{name: "variant", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"2", "33"}, want: "2\n1:12:3|D\n2:33:3|U\n"},
		{name: "variant-declaration", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"1", "12"}, want: "2\n1:12:3|D\n2:33:3|U\n"},
		{name: "missing-field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.missing;}", args: []string{"2", "43"}, want: "0\n"},
		{name: "method-declaration", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"2", "14"}, want: "2\n2:14:3|D\n3:43:3|U\n"},
		{name: "struct-declaration", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"1", "8"}, want: "3\n1:8:1|D\n2:12:1|U\n3:27:1|U\n"},
		{name: "receiver-use", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"2", "31"}, want: "2\n2:10:1|D\n2:31:1|U\n"},
		{name: "enum-declaration", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"1", "6"}, want: "2\n1:6:5|D\n2:27:5|U\n"},
		{name: "loop-declaration", source: "function main():i32{let total=0;for word in [1,2]{total=total+word;}return total;}", args: []string{"1", "37"}, want: "2\n1:37:4|D\n1:63:4|U\n"},
		{name: "match-declaration", source: "function main():i32{let o:Option[i32]=Some(1);return match(o){Some(v)=>v,None=>0};}", args: []string{"1", "68"}, want: "2\n1:68:1|D\n1:72:1|U\n"},
		{name: "lambda-parameter-declaration", source: "function main():i32{let n=1;let f=(k:i32):i32=>k+n;return f(2);}", args: []string{"1", "36"}, want: "2\n1:36:1|D\n1:48:1|U\n"},
		{name: "known-target-missing-declaration", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"2", "28", "hide"}, want: "1\n2:28:3|U\n"},
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lsprefs.fern", "drivers/rundriver.fern")
	writeResolveProject(t, dir, map[string]string{"reference_options.fern": lspReferenceOptionProbe})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "reference_options.fern", "reference_options", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.args...)
					cmd.Stdin = strings.NewReader(tc.source)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("reference options: %v\ngot:\n%swant:\n%s", err, out, tc.want)
					}
				})
			}
		})
	}
}
