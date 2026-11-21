# markdown

A CommonMark 0.31.2 renderer in Fern (#15588), with the command line of
[cmark](https://github.com/commonmark/cmark) 0.31.2. For the same input and
options, its output is the output of cmark in each format of cmark: HTML,
XML, CommonMark, groff man and LaTeX. The port does not copy source code from
cmark.

```
fern -target x86-64-linux -o md examples/markdown/md.fern
printf '# Title\n\n- a\n- b\n' | ./md
printf '# Title\n\n"Hi" -- there...\n' | ./md --to xml --sourcepos --smart
```

`md` reads the files that it gets one after the other, or stdin when it gets
none. It has the options of cmark:

- `--to`, `-t FORMAT`: `html` (the default), `xml`, `man`, `commonmark` or
  `latex`.
- `--width N`: the width where the CommonMark, man and LaTeX renderers wrap a
  line, or 0 for no wrap. `md` reads `N` as C `strtol` does.
- `--sourcepos`: the source position of each block in HTML, and of each node
  in XML.
- `--hardbreaks` and `--nobreaks`: each soft line break is a hard line break,
  or a space.
- `--smart`: curly quotes, en dashes, em dashes and ellipses.
- `--unsafe`: raw HTML, and links to `javascript:`, `vbscript:`, `file:` and
  `data:` URLs.
- `--validate-utf8`: invalid UTF-8 as cmark replaces it (see below).
- `--help`, `-h` and `--version`, with the text of cmark.

For HTML output, pass `--tasklist` to render GitHub-style task list markers
as disabled checkboxes. Pass `--strikethrough` to render paired single or double
tildes as `<del>`. Library callers can set `Options.tasklist` and
`Options.strikethrough` independently.

The usage text names `cmark`, as cmark does for each name of the program
file.

`--safe` is the default. Then, as in cmark, raw HTML is the comment
`<!-- raw HTML omitted -->`, and a link to such a URL has an empty
destination. A `data:` URL of a PNG, GIF, JPEG or WebP image is safe.

An unknown option writes the usage and fails. The errors of `--width` and
`--to`, a file that `md` cannot open, and the exit status are those of cmark.
A directory gives no text, as it does in cmark.

A program can import `"std/markdown"` and call `markdown.to_html(bytes, options)`, or
`markdown.render(builder, bytes, options)` to write into a byte builder.
`markdown.write(writer, bytes, part, options)` gives the HTML to a buffered
writer each time that `part` bytes or more are ready. `md` uses it, so the
HTML of a document is not in memory all at one time. `markdown.options()`
gives the defaults. `render.render(bytes, format, options)` gives the other
formats, and `render.tree(bytes, options)` gives the node tree. The document
is a `u8[]`, and the output is bytes, because the output keeps the bytes of
the document that are not UTF-8.

## How it works

The renderer has the two phases of the appendix of the spec:

1. `blocks.fern` reads the source one line at a time. It keeps a stack of
   the open blocks, and it writes the blocks to a log in the order of the
   document. A container block has a record where it starts and an END
   record where it ends. A leaf block gets its record when it closes. A
   paragraph, a heading and a code block keep no copy of their text. Each
   holds a range of line segments, and each segment is a range of the
   source.
2. `inlines.fern` parses and writes the text of each paragraph and heading.
   `markdown.fern` reads the log in order and writes the HTML.

md4c has the same design. A link reference definition can come after the
link that uses it, so the inline phase starts after the last line. Until
then, the log keeps all the blocks. A record is two i64 fields and a
segment is one i64 field, so the log is small next to the source.

A list is loose when a block in it ends with a blank line and is not its
last block. Each container on the stack keeps what the parser must know of
its last closed child. When the container gets a new child, the parser
knows that the last child is not the last block, and it does the test. A
paragraph that holds only link reference definitions is not a block, so
its test waits until it closes.

The cursor of the line is in local variables, not in the struct of the
parser. A line that cannot start a block does not go through the tests of
the block starts.

The parser reads the bytes of the source as they are. Only the tests that
need a code point decode one, with the UTF-8 rules of cmark in
`std/markdown/utf8`. The writers copy the bytes of the text.

The inline parser reads each byte of the text once. A constant table of 256
entries makes `__scan_set_bytes` a vector scan, and the scan finds the next
byte that can start a token. The scan reads the source past the end of the
text, up to the line ending after it.

A token is a row of four i32 fields. The kinds of token are code spans, raw
HTML, autolinks, runs of `*` and `_`, and the brackets of links and images.
Line breaks, backslash escapes, entity references and the bytes that HTML
escapes are tokens too. The delimiter stack, the bracket stack and the links
are i32 tables, which stay from one paragraph to the next.

Emphasis follows the procedure "process emphasis" of the appendix of the
spec. Each run of delimiters keeps the tags that it opens and closes. The
writer copies the text between two tokens as it is.

The text of a paragraph is a part of the source when only a line feed is
between two of its lines. Other paragraphs, such as one in a block quote,
get one byte array of their lines.

With `--sourcepos`, the block parser keeps the position of each block. As in
cmark, a block ends at the end of the line before the line that closes it.
A fenced code block and a setext heading end at the end of that line. The
length of the line of an ATX heading does not count its closing `#` marks.

The other formats need the node tree of cmark. `render.fern` builds it from
the log of blocks. `inlines.build` adds the nodes of the inline text from
the tokens that the HTML writer reads. `std/markdown/nodes` keeps the tree as rows
of i32 fields, and the text of all the nodes in one byte array.

The tree has the text nodes of cmark. Adjacent text joins. The positions
follow the rules of cmark, such as the column of a string of backticks that
opens no code span.

`xml.fern` writes the XML in parts, as the HTML writer does. `out.fern` is
the writer of the text renderers of cmark. It keeps the prefix of each line
and writes the line breaks that a renderer asks for. It escapes the text for
CommonMark, man or LaTeX, and it wraps the lines. `commonmark.fern`,
`man.fern` and `latex.fern` walk the tree.

`refs.fern` parses the link reference definitions, normalizes link labels,
and scans link destinations and titles. `tags.fern` has the HTML tag
scanners and the start and end conditions of the seven kinds of HTML block.
`std/markdown/entities` has the named character references of HTML and the Unicode
punctuation ranges. `scripts/gen-markdown-tables.py` writes it.

## What it has

- Paragraphs, ATX headings and setext headings.
- Thematic breaks.
- Indented code blocks, and fenced code blocks with an info string.
- Block quotes, with lazy continuation lines.
- Bullet lists and ordered lists, tight and loose.
- The seven kinds of HTML block.
- Link reference definitions.
- Tabs, with a tab stop of four columns.
- Backslash escapes, and entity references: the 2125 names of HTML, decimal
  and hexadecimal. They apply in text, link destinations, link titles and
  info strings.
- Code spans.
- Emphasis and strong emphasis.
- Inline links, and full, collapsed and shortcut reference links. A link
  holds no other link.
- Images, with the text of their content as the `alt` attribute.
- URI and email autolinks.
- Raw HTML: open and closing tags, comments, processing instructions,
  declarations and CDATA sections.
- Hard line breaks and soft line breaks.
- The percent-encoding of destinations and the HTML escapes of cmark.

The renderer skips a byte order mark at the start of the source. It replaces
each NUL byte with U+FFFD.

With `--validate-utf8`, `md` replaces invalid UTF-8 as cmark does. cmark
reads each line without its line ending. It writes one U+FFFD for each of
these sequences:

- a lead byte and the valid continuation bytes after it,
- all the bytes that are left on the line when the sequence is too short
  for them,
- the whole sequence of an overlong form, a surrogate or a code point above
  U+10FFFF.

Without `--validate-utf8`, the bytes that are not UTF-8 stay as they are, as
in cmark. A valid UTF-8 sequence is the shortest form of a code point. That
code point is not a surrogate, and it is not above U+10FFFF. Where cmark reads
a code point, it reads an invalid sequence as follows, and so does `md`:

- Before and after a run of `*`, `_` or a quote, it is a line feed, which
  is whitespace. The character before a run starts at the last byte that is
  not a continuation byte.
- The case folding of a link label gives one U+FFFD for each byte where no
  valid sequence starts. Thus `[a\xFF]` and `[a\xFE]` are the same label.
- The CommonMark, man and LaTeX renderers write a text up to its first
  invalid sequence, and not the rest of it. When a new line starts there,
  the prefix of the line comes first.

## Where cmark does not follow the text of the spec

The port gives the output of cmark 0.31.2 where cmark and the text of the
spec do not agree:

- A line with only a complete open tag of `pre`, `script`, `style` or
  `textarea`, such as `<pre/>`, starts an HTML block of kind 7. cmark and
  commonmark.js do this too.
- A line that can be a lazy continuation line of a paragraph does not start
  an HTML block of kind 7.
- A link label has at most 1000 bytes. The spec gives at most 999
  characters.
- A link title is the longest text that the title can be. A backslash before
  a quote can be text, so `[a](/u "x\")` has the title `x\`.
- The entity references in an autolink apply to its destination and its
  text.

cmark 0.31.2 has these details too:

- A definition keeps a title that is not the end of its line.
- A bracket after a link can open a link: `[a [b](c) [d] e](f)`.
- A comment, a processing instruction or a CDATA section reads a run of
  `-`, `?` or `]` in pairs: `<!-- a ---->` has no end, but `<???>` has.
- After a comment with no end, `<!` starts no raw HTML in the paragraph.
- U+0130 at the start of a line is a digit for the CommonMark renderer.
  cmark tests the low byte of a code point.

## Tests

- The unit cases in `markdown_test.fern` run on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleMarkdownUnit`).
- `TestSelfHostExampleMarkdownSpec` runs the 652 examples of the CommonMark
  0.31.2 spec in `tests/spec.json` (see `licenses/commonmark/NOTICE.txt`)
  with `--unsafe`, as the spec runner of cmark does. It compares the HTML
  exactly, and fails when the set of examples that pass changes. All 652
  examples pass.
- The same test runs the 23 regression tests and the 16 tests of smart
  punctuation of cmark 0.31.2 (`tests/regression.json` and
  `tests/smart_punct.json`). All of them pass.

`TestSelfHostExampleMarkdownAgainstCmark` compares the output of `md` and of
the `cmark` binary on the same input. It needs cmark 0.31.2 on the PATH. The
`port-oracles` action of CI builds it from the tag 0.31.2.

Two tests compare the command line with cmark 0.31.2 in each format:

- `TestSelfHostExampleMarkdownCmarkCases` runs `md` on the recorded cases
  of `tests/cmark.json`.
- `TestSelfHostExampleMarkdownFormats` compares `md` and `cmark` on each
  example of the spec.

The cases of `tests/cmark.json` have each option in each format and the
errors of the command line. They also have invalid UTF-8, with and without
`--validate-utf8`. It is in text, link labels, code, HTML, entity
references, links, delimiter runs, block markers and at the end of the input.
Some join files, and some name a missing file or a directory.

`scripts/gen-markdown-cmark-cases.py` records the stdout, the stderr and
the exit status of cmark 0.31.2 for each case. The test runs `md` on
x86-64, arm64 and wasm32-wasi, and compares the three. When `cmark` is on
the PATH, it also makes sure that the record is still the output of cmark.

`TestSelfHostExampleMarkdownFormats` gives each example of the spec to `md`
on each target and to `cmark`. It uses each format, with 11 sets of options.
The output of each example is the same.

The pathological tests of cmark 0.31.2 (`test/pathological_tests.py
--program md`) pass too. One of them has 30000 blank lines in a list of
depth 30000. As in cmark, a second blank line stops at the first list. Thus
it does not visit each block of the list.

## Speed

Measured 2026-11-15 on the 4-core x86-64 container, with other work on the
machine: the median of 15 runs, and the peak memory. Each program reads the
input from a file and writes to `/dev/null`. Both `md` and cmark use their
default options.

The first input is the Markdown files of the repository (`git ls-files
'*.md'`) one after the other, 24.6 MB. The second input is the 652 spec
examples, each with a blank line after it, repeated to 16.9 MB.

| input | format | `md` | cmark 0.31.2 |
|---|---|---:|---:|
| the repository, 24.6 MB | HTML | 0.126 s, 38 MB | 0.449 s, 152 MB |
| the repository, 24.6 MB | `--to xml` | 0.295 s, 222 MB | 0.508 s, 175 MB |
| the repository, 24.6 MB | `--to commonmark` | 0.515 s, 244 MB | 0.819 s, 149 MB |
| the spec examples, 16.9 MB | HTML | 0.402 s, 76 MB | 1.174 s, 399 MB |
| the spec examples, 16.9 MB | `--to xml` | 0.925 s, 388 MB | 1.325 s, 466 MB |
| the spec examples, 16.9 MB | `--to commonmark` | 0.936 s, 390 MB | 1.441 s, 389 MB |

This machine has no md4c now. On 2026-11-13, the repository was 23.5 MB.
md4c 0.4.8 then took 0.125 s and 29 MB for its HTML. For the HTML of the
spec input, it took 0.390 s and 44 MB. A small driver of `md_html` with `MD_DIALECT_COMMONMARK` ran it, and wrote each part
of the HTML when md4c gave it. In the same runs, `md` took 0.112 s and
0.356 s.

The HTML does not use the node tree. callgrind counts the instructions of
`md` before and after it kept the bytes of the source as they are. On the
first 6 MB of the repository input, the HTML takes 203.7 M against 211.4 M.
XML takes 686 M against 697 M, and CommonMark takes 993 M against 1012 M.
On the first 4 MB of the spec input, the HTML takes 826 M against 843 M.
The other formats keep the node tree and its text, so they use more memory
than cmark.

A repeat on 2026-11-21 used the HTML command lines of all three renderers on
the same x86-64 Linux host. The table gives median elapsed milliseconds over
15 measured runs per program after a warmup. The programs shared one CPU
affinity, rotated their order and wrote stdout to /dev/null.

| HTML input | Bytes | Fern md | cmark 0.31.2 | md4c 0.4.8 (md2html) |
|---|---:|---:|---:|---:|
| Repository Markdown | 25,617,121 | 86.2 ms | 353.3 ms | 107.3 ms |
| Repeated spec examples | 16,888,143 | 246.2 ms | 1060.9 ms | 276.9 ms |

The repository input contains all 2,859 regular Markdown files from
`git archive c507562f85 -- '*.md'` in archive order, with two newlines
after each file. For the spec input, append two newlines to the Markdown
of each of the 652 examples in `tests/spec.json`, then repeat that block
as many whole times as fit in 16,900,000 bytes. Fern and cmark produced
byte-identical HTML on both inputs. The C tools used release-tag sources
compiled with `cc -O3 -DNDEBUG`. Fern used `-target x86-64-linux`. Each
renderer received the input filename and used its default options.
