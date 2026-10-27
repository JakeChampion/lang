package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const lspBindingOptionProbe = `import "./lspquery";
import "./lexical";
import "./parser";
import "./lexer";
import "./util";
import "./drivers/rundriver";

function main():i32 {
 let source=rundriver.read_stdin("binding_probe");
 let mod=parser.parse_module(lexer.tokenize(source));
 let p=lspquery.viewed(lspquery.program(mod,mod,[],[source]));
 let av=args();let line=util.digits_to_i32(av[1]);let col=util.digits_to_i32(av[2]);
 let q:lspquery.Query=match(lspquery.query(p,line,col)){Some(value)=>value,None=>{print("NO_SITE");return 0;}};
 let f:lspquery.Found=match(q.f){Some(value)=>value,None=>{print("UNLOCATED");return 0;}};
 let fd=q.v.mod.funcs[f.fd];
 let b:lexical.Binding=match(lspquery.binding_of(fd,q.site)){Some(value)=>value,None=>{print("NONE");return 0;}};
 print(util.i32_to_string(b.id)+"|"+b.name+"|"+b.symbol+"|"+util.i32_to_string(b.line)+":"+util.i32_to_string(b.col)+"|"+if(lspquery.is_param(b,fd)){"PARAM"}else{"LOCAL"});
 return 0;
}
`

func TestSelfHostLSPBindingOptions(t *testing.T) {
	cases := []struct {
		name, source string
		args         []string
		want         string
	}{
		{name: "function-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"2", "28"}, want: "NONE\n"},
		{name: "parameter-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"1", "32"}, want: "0|a|$binding$0$a|1:1|PARAM\n"},
		{name: "local-use", source: "function main():i32{let n=3;return n;}", args: []string{"1", "36"}, want: "0|n|$binding$0$n|1:21|LOCAL\n"},
		{name: "local-declaration", source: "function main():i32{let n=3;return n;}", args: []string{"1", "25"}, want: "UNLOCATED\n"},
		{name: "parameter-declaration", source: "function add(a:i32):i32{return a;}", args: []string{"1", "14"}, want: "UNLOCATED\n"},
		{name: "function-declaration", source: "function add(a:i32):i32{return a;}", args: []string{"1", "10"}, want: "UNLOCATED\n"},
		{name: "shadow", source: "function main():i32{let n=3;{let n=\"hi\";print(n);}return n;}", args: []string{"1", "47"}, want: "1|n|$binding$1$n|1:30|LOCAL\n"},
		{name: "generic", source: "function id[T](a:T):T{return a;}\nfunction main():i32{return id(1);}", args: []string{"2", "28"}, want: "NONE\n"},
		{name: "struct-type", source: "struct P{n:i32}\nfunction main():i32{let p:P=P{n:1};return p.n;}", args: []string{"2", "27"}, want: "UNLOCATED\n"},
		{name: "struct-value", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"2", "27"}, want: "NONE\n"},
		{name: "field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"2", "43"}, want: "NONE\n"},
		{name: "field-declaration", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"1", "10"}, want: "UNLOCATED\n"},
		{name: "method", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"3", "43"}, want: "NONE\n"},
		{name: "method-value", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};let f=p.sum;return 0;}", args: []string{"3", "42"}, want: "NONE\n"},
		{name: "builtin-string", source: "function main():i32{let s=\"abc\";return s.len();}", args: []string{"1", "42"}, want: "NONE\n"},
		{name: "builtin-array", source: "function main():i32{let a=[1,2];return a.len();}", args: []string{"1", "42"}, want: "NONE\n"},
		{name: "variant", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"2", "33"}, want: "NONE\n"},
		{name: "variant-declaration", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"1", "12"}, want: "UNLOCATED\n"},
		{name: "enum-type", source: "enum Color{Red,Green}\nfunction pick(c:Color):i32{return 0;}", args: []string{"2", "17"}, want: "UNLOCATED\n"},
		{name: "missing-name", source: "function main():i32{return missing;}", args: []string{"1", "28"}, want: "NONE\n"},
		{name: "missing-field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.missing;}", args: []string{"2", "43"}, want: "NONE\n"},
		{name: "docs", source: "// Add one.\nfunction add(a:i32):i32{return a+1;}\nfunction main():i32{return add(1);}", args: []string{"3", "28"}, want: "NONE\n"},
		{name: "utf8-prefix", source: "function main():i32{let s=\"\u00e9\u2192🐛\";let n=1;return n;}", args: []string{"1", "54"}, want: "1|n|$binding$1$n|1:39|LOCAL\n"},
		{name: "zero-width", source: "function main():i32{return 0;}", args: []string{"1", "31"}, want: "NO_SITE\n"},
		{name: "incomplete", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(", args: []string{"2", "28"}, want: "UNLOCATED\n"},
		{name: "empty", source: "", args: []string{"1", "1"}, want: "NO_SITE\n"},
		{name: "missing-receiver", source: ".foo", args: []string{"1", "2"}, want: "NO_SITE\n"},
		{name: "loop-binding", source: "function main():i32{let total=0;for word in [1,2]{total=total+word;}return total;}", args: []string{"1", "63"}, want: "1|word|$binding$1$word|1:0|LOCAL\n"},
		{name: "loop-outer-update", source: "function main():i32{let total=0;for word in [1,2]{total=total+word;}return total;}", args: []string{"1", "57"}, want: "0|total|$binding$0$total|1:21|LOCAL\n"},
		{name: "loop-after", source: "function main():i32{let total=0;for word in [1,2]{total=total+word;}return total;}", args: []string{"1", "76"}, want: "0|total|$binding$0$total|1:21|LOCAL\n"},
		{name: "lambda-parameter", source: "function main():i32{let n=1;let f=(k:i32):i32=>k+n;return f(2);}", args: []string{"1", "48"}, want: "1|k|$binding$1$k|1:35|PARAM\n"},
		{name: "lambda-capture", source: "function main():i32{let n=1;let f=(k:i32):i32=>k+n;return f(2);}", args: []string{"1", "50"}, want: "0|n|$binding$0$n|1:21|LOCAL\n"},
		{name: "lambda-local-function", source: "function main():i32{let n=1;let f=(k:i32):i32=>k+n;return f(2);}", args: []string{"1", "59"}, want: "2|f|$binding$2$f|1:29|LOCAL\n"},
		{name: "lambda-shadow", source: "function main():i32{let n=1;let f=(n:i32):i32=>n;return n;}", args: []string{"1", "48"}, want: "1|n|$binding$1$n|1:35|PARAM\n"},
		{name: "lambda-after-shadow", source: "function main():i32{let n=1;let f=(n:i32):i32=>n;return n;}", args: []string{"1", "57"}, want: "0|n|$binding$0$n|1:21|LOCAL\n"},
		{name: "match-binding", source: "function main():i32{let o:Option[i32]=Some(1);return match(o){Some(v)=>v,None=>0};}", args: []string{"1", "72"}, want: "1|v|$binding$1$v|1:63|LOCAL\n"},
		{name: "match-scrutinee", source: "function main():i32{let o:Option[i32]=Some(1);return match(o){Some(v)=>v,None=>0};}", args: []string{"1", "60"}, want: "0|o|$binding$0$o|1:21|LOCAL\n"},
		{name: "global-constant", source: "const LIMIT:i32=10;\nfunction main():i32{return LIMIT;}", args: []string{"2", "28"}, want: "NONE\n"},
		{name: "parameter-shadow", source: "function f(x:i32):i32{let x=\"hi\";return x.len();}", args: []string{"1", "41"}, want: "1|x|$binding$1$x|1:23|LOCAL\n"},
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lspquery.fern", "drivers/rundriver.fern")
	writeResolveProject(t, dir, map[string]string{"binding_options.fern": lspBindingOptionProbe})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "binding_options.fern", "binding_options", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.args...)
					cmd.Stdin = strings.NewReader(tc.source)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("binding options: %v\ngot:\n%swant:\n%s", err, out, tc.want)
					}
				})
			}
		})
	}
}
