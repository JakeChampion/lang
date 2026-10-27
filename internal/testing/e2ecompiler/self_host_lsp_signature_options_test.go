package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"strings"
	"testing"
)

const lspSignatureOptionProbe = `import "./lspassist";
import "./lspquery";
import "./parser";
import "./lexer";
import "./util";
import "./drivers/rundriver";

function emit_signature(sh:lspassist.Signature):void {
 let text=sh.label+"|"+util.i32_to_string(sh.active);
 for i in 0..sh.starts.len(){text=text+"|"+util.i32_to_string(sh.starts[i])+":"+util.i32_to_string(sh.ends[i]);}
 print(text);
}
function emit_parts(parts:lspquery.Parts):void {
 let text="FOUND|"+parts.ret;
 for label in parts.params{text=text+"|"+label;}
 print(text);
}
function main():i32 {
 let source=rundriver.read_stdin("signature_probe");
 let mod=parser.parse_module(lexer.tokenize(source));
 let p=lspquery.program(mod,mod,[],[source]);
 let av=args();let line=util.digits_to_i32(av[1]);let col=util.digits_to_i32(av[2]);
match(lspassist.signature_help(p,line,col)){Some(sh)=>{emit_signature(sh);},None=>{print("NONE");}}
 let v=lspquery.view_of(p);let toks=lspquery.module_toks(p,0);let quals=lspquery.quals_of(v,0);
 let site:lspquery.Site=match(lspquery.site_at(toks,quals,util.digits_to_i32(av[3]),util.digits_to_i32(av[4]))){Some(value)=>value,None=>{print("NO_SITE");return 0;}};
 match(lspquery.call_parts(v.types,site)){Some(parts)=>{match(parts){Some(value)=>{emit_parts(value);},None=>{print("REFUSED");}}},None=>{print("UNLOCATED");}}
return 0;
}
`

func TestSelfHostLSPSignatureOptions(t *testing.T) {
	cases := []struct {
		name, source string
		args         []string
		want         string
	}{
		{name: "ordinary", source: "function add(a:i32,b:i32):i32{return a+b;}\nfunction main():i32{return add(1,2);}", args: []string{"2", "34", "2", "28"}, want: "function add(i32, i32): i32|1|13:16|18:21\nFOUND|i32|i32|i32\n"},
		{name: "first-argument", source: "function add(a:i32,b:i32):i32{return a+b;}\nfunction main():i32{return add(1,2);}", args: []string{"2", "32", "2", "28"}, want: "function add(i32, i32): i32|0|13:16|18:21\nFOUND|i32|i32|i32\n"},
		{name: "zero-arguments", source: "function ping():i32{return 0;}\nfunction main():i32{return ping();}", args: []string{"2", "33", "2", "28"}, want: "function ping(): i32|0\nFOUND|i32\n"},
		{name: "unlocated-fallback", source: "function add(a:i32):i32{return a;}\nadd(1);", args: []string{"2", "5", "2", "1"}, want: "function add(i32): i32|0|13:16\nUNLOCATED\n"},
		{name: "shadow-refusal", source: "function add(a:i32):i32{return a;}\nfunction main():i32{let add=0;return add(1);}", args: []string{"2", "42", "2", "38"}, want: "NONE\nREFUSED\n"},
		{name: "callable-shadow-refusal", source: "function add(a:i32):i32{return a;}\nfunction main():i32{let add=(x:i32):i32=>x;return add(1);}", args: []string{"2", "55", "2", "51"}, want: "NONE\nREFUSED\n"},
		{name: "unknown", source: "function main():i32{return unknown(1);}", args: []string{"1", "36", "1", "28"}, want: "NONE\nREFUSED\n"},
		{name: "nested", source: "function add(a:i32,b:i32):i32{return a+b;}\nfunction main():i32{return add(add(1,2),3);}", args: []string{"2", "38", "2", "32"}, want: "function add(i32, i32): i32|1|13:16|18:21\nFOUND|i32|i32|i32\n"},
		{name: "generic", source: "function id[T](a:T):T{return a;}\nfunction main():i32{return id(1);}", args: []string{"2", "31", "2", "28"}, want: "function id(T): T|0|12:13\nFOUND|0_T|0_T\n"},
		{name: "method", source: "struct P{n:i32}\nfunction(p:P)sum(a:i32):i32{return p.n+a;}\nfunction main():i32{let p=P{n:2};return p.sum(1);}", args: []string{"3", "47", "3", "43"}, want: "function sum(i32): i32|0|13:16\nFOUND|i32|i32\n"},
		{name: "builtin-string", source: "function main():i32{let s=\"abc\";return s.len();}", args: []string{"1", "46", "1", "42"}, want: "function len(): i32|0\nFOUND|i32\n"},
		{name: "builtin-array", source: "function main():i32{let a=[1,2];return a.len();}", args: []string{"1", "46", "1", "42"}, want: "function len(): i32|0\nFOUND|i32\n"},
		{name: "member-refusal", source: "function missing(a:i32):i32{return a;}\nfunction main():i32{let s=\"abc\";return s.missing(1);}", args: []string{"2", "50", "2", "42"}, want: "NONE\nREFUSED\n"},
		{name: "utf8-string", source: "function add(s:string,n:i32):i32{return n;}\nfunction main():i32{return add(\"é→🐛\",1);}", args: []string{"2", "44", "2", "28"}, want: "function add(string, i32): i32|1|13:19|21:24\nFOUND|i32|string|i32\n"},
		{name: "comma-string", source: "function add(s:string,n:i32):i32{return n;}\nfunction main():i32{return add(\"a,b,c\",1);}", args: []string{"2", "40", "2", "28"}, want: "function add(string, i32): i32|1|13:19|21:24\nFOUND|i32|string|i32\n"},
		{name: "excess-arguments", source: "function add(a:i32,b:i32):i32{return a+b;}\nfunction main():i32{return add(1,2,3);}", args: []string{"2", "36", "2", "28"}, want: "function add(i32, i32): i32|1|13:16|18:21\nFOUND|i32|i32|i32\n"},
		{name: "unfinished", source: "function add(a:i32,b:i32):i32{return a+b;}\nfunction main():i32{return add(1,", args: []string{"2", "34", "2", "28"}, want: "function add(i32, i32): i32|1|13:16|18:21\nUNLOCATED\n"},
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lspassist.fern", "drivers/rundriver.fern")
	writeResolveProject(t, dir, map[string]string{"signature_options.fern": lspSignatureOptionProbe})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "signature_options.fern", "signature_options", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.args...)
					cmd.Stdin = strings.NewReader(tc.source)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("signature options: %v\ngot:\n%swant:\n%s", err, out, tc.want)
					}
				})
			}
		})
	}
}
