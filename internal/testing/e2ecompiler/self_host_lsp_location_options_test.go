package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"strings"
	"testing"
)

const lspLocationOptionProbe = `import "./lspquery";
import "./lsprefs";
import "./lsptokens";
import "./parser";
import "./lexer";
import "./docsummary";
import "./util";
import "./drivers/rundriver";

function coords(line:i32,col:i32,width:i32):string{return util.i32_to_string(line)+":"+util.i32_to_string(col)+":"+util.i32_to_string(width);}
function hover_text(h:lspquery.Hover):string{return docsummary.quote(h.text)+"|"+coords(h.line,h.col,h.len);}
function span_text(s:lspquery.SourceSpan):string{return coords(s.line,s.col,s.len);}
function main():i32 {
 let source=rundriver.read_stdin("location_probe");
 let mod=parser.parse_module(lexer.tokenize(source));
 let p=lspquery.program(mod,mod,[],[source]);
 let av=args();let line=util.digits_to_i32(av[1]);let col=util.digits_to_i32(av[2]);
 print("H|"+match(lspquery.hover(p,line,col)){Some(h)=>hover_text(h),None=>"NONE"});
 print("D|"+match(lspquery.definition(p,line,col)){Some(d)=>span_text(d),None=>"NONE"});
 print("ZERO|"+match(lspquery.decl_def(p,1,1,"")){Some(s)=>span_text(s),None=>"NONE"});
 print("ABSENT|"+match(lspquery.decl_def(p,0,0,"missing")){Some(s)=>span_text(s),None=>"NONE"});
 print("DOC|"+docsummary.quote(lspquery.hover_doc(p,line,col)));
 let refs=lsprefs.references(p,line,col);print("REFS|"+util.i32_to_string(refs.len()));
 for ref in refs {print(coords(ref.line,ref.col,ref.len)+"|"+if(ref.decl){"D"}else{"U"});}
 let tokens=lsptokens.semantic_tokens(p,0);print("TOKENS|"+util.i32_to_string(tokens.len()));
 for token in tokens {print(coords(token.line,token.col,token.len)+"|"+util.i32_to_string(token.kind));}
 return 0;
}
`

const lspMissingDeclarationContract = `import "./lspquery";
import "./lsprefs";
import "./parser";
import "./lexer";

function main():i32 {
 let source="function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}";
 let parsed=parser.parse_module(lexer.tokenize(source));
 let hidden=parser.FuncDecl{...parsed.funcs[0],line:0,col:0};
 let mod=parser.Module{...parsed,funcs:parsed.funcs.with(0,hidden)};
 let p=lspquery.program(mod,mod,[],[source]);
 match(lspquery.definition(p,2,28)){Some(_)=>{return 1;},None=>{}}
 let refs=lsprefs.references(p,2,28);
 if(refs.len()!=1){print(refs.len());return 2;}
 if(refs[0].decl || refs[0].line!=2 || refs[0].col!=28 || refs[0].len!=3){return 3;}
 return 0;
}
`

func TestSelfHostLSPLocationOptions(t *testing.T) {
	cases := []struct {
		name, source string
		args         []string
		want         string
	}{
		{name: "function-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"2", "28"}, want: "H|\"function add(i32): i32\"|2:28:3\nD|1:10:3\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:10:3|D\n2:28:3|U\nTOKENS|5\n1:10:3|0\n1:14:1|5\n1:32:1|5\n2:10:4|0\n2:28:3|0\n"},
		{name: "parameter-use", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(1);}", args: []string{"1", "32"}, want: "H|\"(parameter) a: i32\"|1:32:1\nD|1:14:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:14:1|D\n1:32:1|U\nTOKENS|5\n1:10:3|0\n1:14:1|5\n1:32:1|5\n2:10:4|0\n2:28:3|0\n"},
		{name: "local-use", source: "function main():i32{let n=3;return n;}", args: []string{"1", "36"}, want: "H|\"(var) n: i32\"|1:36:1\nD|1:25:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:25:1|D\n1:36:1|U\nTOKENS|3\n1:10:4|0\n1:25:1|6\n1:36:1|6\n"},
		{name: "local-declaration", source: "function main():i32{let n=3;return n;}", args: []string{"1", "25"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:25:1|D\n1:36:1|U\nTOKENS|3\n1:10:4|0\n1:25:1|6\n1:36:1|6\n"},
		{name: "parameter-declaration", source: "function add(a:i32):i32{return a;}", args: []string{"1", "14"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:14:1|D\n1:32:1|U\nTOKENS|3\n1:10:3|0\n1:14:1|5\n1:32:1|5\n"},
		{name: "function-declaration", source: "function add(a:i32):i32{return a;}", args: []string{"1", "10"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|1\n1:10:3|D\nTOKENS|3\n1:10:3|0\n1:14:1|5\n1:32:1|5\n"},
		{name: "shadow", source: "function main():i32{let n=3;{let n=\"hi\";print(n);}return n;}", args: []string{"1", "47"}, want: "H|\"(var) n: string\"|1:47:1\nD|1:34:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:34:1|D\n1:47:1|U\nTOKENS|6\n1:10:4|0\n1:25:1|6\n1:34:1|6\n1:41:5|0\n1:47:1|6\n1:58:1|6\n"},
		{name: "generic", source: "function id[T](a:T):T{return a;}\nfunction main():i32{return id(1);}", args: []string{"2", "28"}, want: "H|\"function id(T): T\"|2:28:2\nD|1:10:2\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:10:2|D\n2:28:2|U\nTOKENS|5\n1:10:2|0\n1:16:1|5\n1:30:1|5\n2:10:4|0\n2:28:2|0\n"},
		{name: "struct-type", source: "struct P{n:i32}\nfunction main():i32{let p:P=P{n:1};return p.n;}", args: []string{"2", "27"}, want: "H|\"struct P { n: i32 }\"|2:27:1\nD|1:8:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|3\n1:8:1|D\n2:27:1|U\n2:29:1|U\nTOKENS|9\n1:8:1|2\n1:10:1|7\n2:10:4|0\n2:25:1|6\n2:27:1|2\n2:29:1|2\n2:31:1|7\n2:43:1|6\n2:45:1|7\n"},
		{name: "struct-value", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"2", "27"}, want: "H|\"struct P { n: i32 }\"|2:27:1\nD|1:8:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:8:1|D\n2:27:1|U\nTOKENS|8\n1:8:1|2\n1:10:1|7\n2:10:4|0\n2:25:1|6\n2:27:1|2\n2:29:1|7\n2:41:1|6\n2:43:1|7\n"},
		{name: "field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"2", "43"}, want: "H|\"(field) n: i32\"|2:43:1\nD|1:10:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|3\n1:10:1|D\n2:29:1|U\n2:43:1|U\nTOKENS|8\n1:8:1|2\n1:10:1|7\n2:10:4|0\n2:25:1|6\n2:27:1|2\n2:29:1|7\n2:41:1|6\n2:43:1|7\n"},
		{name: "field-declaration", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.n;}", args: []string{"1", "10"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|3\n1:10:1|D\n2:29:1|U\n2:43:1|U\nTOKENS|8\n1:8:1|2\n1:10:1|7\n2:10:4|0\n2:25:1|6\n2:27:1|2\n2:29:1|7\n2:41:1|6\n2:43:1|7\n"},
		{name: "method", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};return p.sum();}", args: []string{"3", "43"}, want: "H|\"(method on P) function sum(): i32\"|3:43:3\nD|2:14:3\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n2:14:3|D\n3:43:3|U\nTOKENS|13\n1:8:1|2\n1:10:1|7\n2:10:1|5\n2:12:1|2\n2:14:3|1\n2:31:1|5\n2:33:1|7\n3:10:4|0\n3:25:1|6\n3:27:1|2\n3:29:1|7\n3:41:1|6\n3:43:3|1\n"},
		{name: "method-value", source: "struct P{n:i32}\nfunction(p:P)sum():i32{return p.n;}\nfunction main():i32{let p=P{n:1};let f=p.sum;return 0;}", args: []string{"3", "42"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|14\n1:8:1|2\n1:10:1|7\n2:10:1|5\n2:12:1|2\n2:14:3|1\n2:31:1|5\n2:33:1|7\n3:10:4|0\n3:25:1|6\n3:27:1|2\n3:29:1|7\n3:38:1|6\n3:40:1|6\n3:42:3|7\n"},
		{name: "builtin-string", source: "function main():i32{let s=\"abc\";return s.len();}", args: []string{"1", "42"}, want: "H|\"(method on string) function len(): i32\"|1:42:3\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|4\n1:10:4|0\n1:25:1|6\n1:40:1|6\n1:42:3|1\n"},
		{name: "builtin-array", source: "function main():i32{let a=[1,2];return a.len();}", args: []string{"1", "42"}, want: "H|\"(method on Array) function len(): i32\"|1:42:3\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|4\n1:10:4|0\n1:25:1|6\n1:40:1|6\n1:42:3|1\n"},
		{name: "variant", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"2", "33"}, want: "H|\"(variant of Color) Red\"|2:33:3\nD|1:12:3\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:12:3|D\n2:33:3|U\nTOKENS|7\n1:6:5|3\n1:12:3|4\n1:16:5|4\n2:10:4|0\n2:25:1|6\n2:27:5|3\n2:33:3|4\n"},
		{name: "variant-declaration", source: "enum Color{Red,Green}\nfunction main():i32{let c=Color.Red;return 0;}", args: []string{"1", "12"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:12:3|D\n2:33:3|U\nTOKENS|7\n1:6:5|3\n1:12:3|4\n1:16:5|4\n2:10:4|0\n2:25:1|6\n2:27:5|3\n2:33:3|4\n"},
		{name: "enum-type", source: "enum Color{Red,Green}\nfunction pick(c:Color):i32{return 0;}", args: []string{"2", "17"}, want: "H|\"enum Color { Red, Green }\"|2:17:5\nD|1:6:5\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:6:5|D\n2:17:5|U\nTOKENS|6\n1:6:5|3\n1:12:3|4\n1:16:5|4\n2:10:4|0\n2:15:1|5\n2:17:5|3\n"},
		{name: "missing-name", source: "function main():i32{return missing;}", args: []string{"1", "28"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|1\n1:10:4|0\n"},
		{name: "missing-field", source: "struct P{n:i32}\nfunction main():i32{let p=P{n:1};return p.missing;}", args: []string{"2", "43"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|8\n1:8:1|2\n1:10:1|7\n2:10:4|0\n2:25:1|6\n2:27:1|2\n2:29:1|7\n2:41:1|6\n2:43:7|7\n"},
		{name: "docs", source: "// Add one.\nfunction add(a:i32):i32{return a+1;}\nfunction main():i32{return add(1);}", args: []string{"3", "28"}, want: "H|\"function add(i32): i32\"|3:28:3\nD|2:10:3\nZERO|1:1:0\nABSENT|NONE\nDOC|\"Add one.\"\nREFS|2\n2:10:3|D\n3:28:3|U\nTOKENS|5\n2:10:3|0\n2:14:1|5\n2:32:1|5\n3:10:4|0\n3:28:3|0\n"},
		{name: "utf8-prefix", source: "function main():i32{let s=\"é→🐛\";let n=1;return n;}", args: []string{"1", "54"}, want: "H|\"(var) n: i32\"|1:54:1\nD|1:43:1\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|2\n1:43:1|D\n1:54:1|U\nTOKENS|4\n1:10:4|0\n1:25:1|6\n1:43:1|6\n1:54:1|6\n"},
		{name: "zero-width", source: "function main():i32{return 0;}", args: []string{"1", "31"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|1\n1:10:4|0\n"},
		{name: "incomplete", source: "function add(a:i32):i32{return a;}\nfunction main():i32{return add(", args: []string{"2", "28"}, want: "H|NONE\nD|NONE\nZERO|1:1:0\nABSENT|NONE\nDOC|\"\"\nREFS|0\nTOKENS|4\n1:10:3|0\n1:14:1|5\n1:32:1|5\n2:10:4|0\n"},
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lspquery.fern", "lsprefs.fern", "lsptokens.fern", "drivers/rundriver.fern")
	writeResolveProject(t, dir, map[string]string{"location_options.fern": lspLocationOptionProbe, "missing_declaration.fern": lspMissingDeclarationContract})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "location_options.fern", "location_options", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.args...)
					cmd.Stdin = strings.NewReader(tc.source)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("location options: %v\ngot:\n%swant:\n%s", err, out, tc.want)
					}
				})
			}
			t.Run("missing-declaration", func(t *testing.T) {
				contract := buildSelfHostBinFor(t, dir, "missing_declaration.fern", "missing_declaration", target)
				out, err := mvsDriverCommand(t, target, contract).CombinedOutput()
				if err != nil || len(out) != 0 {
					t.Fatalf("name target without source coordinates: %v\n%s", err, out)
				}
			})
		})
	}
}
