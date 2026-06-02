package literate

import (
	"fmt"
	"regexp"
	"strings"
)

// htmlEscape escapes the five characters that are unsafe in HTML text /
// attribute context.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// ── prose: a small Markdown subset → HTML ──────────────────────────
//
// Supported: ATX headings (`#`..`######`), thematic breaks (`---` /
// `***`), unordered (`-`/`*`/`+`) and ordered (`1.`) lists,
// blockquotes (`>`), paragraphs, and the inline spans `code`,
// **bold**, *italic*, and [text](url). It is deliberately small — the
// value WeaveHTML adds is the linked, highlighted code chunks, not a
// full CommonMark engine.

var (
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reOrdMd  = regexp.MustCompile(`^(\d+)\.\s+(.*)`)
)

func markdownToHTML(prose string) string {
	lines := strings.Split(prose, "\n")
	var b strings.Builder
	var para []string
	flushPara := func() {
		if len(para) == 0 {
			return
		}
		fmt.Fprintf(&b, "<p>%s</p>\n", inlineMarkdown(strings.Join(para, " ")))
		para = nil
	}
	i := 0
	for i < len(lines) {
		line := lines[i]
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flushPara()
			i++
		case fenceRunLen(t) >= 3:
			// Fenced code block (```lang … ```). Non-fern fences in a
			// document — `sh`, `markdown`, … — are carried as prose, so
			// WeaveHTML must render them, not split on their backticks.
			flushPara()
			open := fenceRunLen(t)
			lang := strings.TrimSpace(t[open:])
			i++
			var code []string
			for i < len(lines) {
				if c := strings.TrimSpace(lines[i]); fenceRunLen(c) >= open && c == strings.Repeat("`", len(c)) {
					i++
					break
				}
				code = append(code, lines[i])
				i++
			}
			b.WriteString(`<pre class="code display"><code>`)
			for _, cl := range code {
				if lang == "fern" {
					b.WriteString(highlightFern(cl))
				} else {
					b.WriteString(htmlEscape(cl))
				}
				b.WriteByte('\n')
			}
			b.WriteString("</code></pre>\n")
		case isThematicBreak(t):
			flushPara()
			b.WriteString("<hr>\n")
			i++
		case headingLevel(t) > 0:
			flushPara()
			n := headingLevel(t)
			text := strings.TrimSpace(t[n+1:])
			fmt.Fprintf(&b, "<h%d id=%q>%s</h%d>\n", n, slugify(text), inlineMarkdown(text), n)
			i++
		case strings.HasPrefix(t, "> "):
			flushPara()
			var quote []string
			for i < len(lines) {
				q := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(q, ">") {
					break
				}
				quote = append(quote, inlineMarkdown(strings.TrimSpace(strings.TrimPrefix(q, ">"))))
				i++
			}
			fmt.Fprintf(&b, "<blockquote>%s</blockquote>\n", strings.Join(quote, " "))
		case isUnorderedItem(t):
			flushPara()
			b.WriteString("<ul>\n")
			for i < len(lines) && isUnorderedItem(strings.TrimSpace(lines[i])) {
				item := strings.TrimSpace(lines[i])
				fmt.Fprintf(&b, "<li>%s</li>\n", inlineMarkdown(strings.TrimSpace(item[1:])))
				i++
			}
			b.WriteString("</ul>\n")
		case reOrdMd.MatchString(t):
			flushPara()
			b.WriteString("<ol>\n")
			for i < len(lines) {
				m := reOrdMd.FindStringSubmatch(strings.TrimSpace(lines[i]))
				if m == nil {
					break
				}
				fmt.Fprintf(&b, "<li>%s</li>\n", inlineMarkdown(m[2]))
				i++
			}
			b.WriteString("</ol>\n")
		default:
			para = append(para, t)
			i++
		}
	}
	flushPara()
	return b.String()
}

// fenceRunLen returns the length of a leading run of backticks (a code
// fence opener/closer), or 0 if the trimmed line doesn't start with one.
func fenceRunLen(t string) int {
	n := 0
	for n < len(t) && t[n] == '`' {
		n++
	}
	return n
}

// slugify turns heading text into a URL-safe anchor id: lowercase,
// runs of non-alphanumerics collapsed to single dashes, trimmed. Inline
// markup is ignored (it's stripped of its markers by the alnum filter).
// Duplicate heading texts produce duplicate ids — rare in practice, and
// browsers simply jump to the first.
func slugify(text string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func headingLevel(t string) int {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(t) && t[n] == ' ' {
		return n
	}
	return 0
}

func isThematicBreak(t string) bool {
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

func isUnorderedItem(t string) bool {
	return len(t) >= 2 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' '
}

// inlineMarkdown renders the inline spans of one logical line. Code
// spans are extracted first (so emphasis inside them is left alone) and
// honour CommonMark's variable-length delimiters — a run of N backticks
// opens a span closed by the next run of exactly N — so inline code that
// itself contains backticks (e.g. ```` ```fern ```` ) renders intact.
// Every other segment is HTML-escaped and run through the
// link/bold/italic replacements.
func inlineMarkdown(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			j := i
			for j < len(s) && s[j] != '`' {
				j++
			}
			b.WriteString(renderEmphasis(s[i:j]))
			i = j
			continue
		}
		n := backtickRun(s, i)
		if close := findCloser(s, i+n, n); close >= 0 {
			fmt.Fprintf(&b, "<code>%s</code>", htmlEscape(trimCodeSpan(s[i+n:close])))
			i = close + n
			continue
		}
		// No matching closer — the backticks are literal text.
		b.WriteString(s[i : i+n])
		i += n
	}
	return b.String()
}

func renderEmphasis(seg string) string {
	seg = htmlEscape(seg)
	seg = reLink.ReplaceAllString(seg, `<a href="$2">$1</a>`)
	seg = reBold.ReplaceAllString(seg, `<strong>$1</strong>`)
	seg = reItalic.ReplaceAllString(seg, `<em>$1</em>`)
	return seg
}

// backtickRun returns the length of the backtick run starting at i.
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// findCloser returns the index of the start of the next backtick run of
// length exactly want at/after from, or -1. Longer runs don't close.
func findCloser(s string, from, want int) int {
	for i := from; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		run := backtickRun(s, i)
		if run == want {
			return i
		}
		i += run - 1
	}
	return -1
}

// trimCodeSpan applies CommonMark's one-space strip: a code span with a
// leading and trailing space (and some non-space content) drops one of
// each, so “ ` “...“ ` “ renders without the padding.
func trimCodeSpan(c string) string {
	if len(c) >= 2 && c[0] == ' ' && c[len(c)-1] == ' ' && strings.TrimSpace(c) != "" {
		return c[1 : len(c)-1]
	}
	return c
}

// ── code: light Fern syntax highlighting ───────────────────────────

var fernKeywords = map[string]bool{
	"function": true, "fn": true, "var": true, "return": true, "if": true,
	"else": true, "while": true, "for": true, "in": true, "match": true,
	"struct": true, "enum": true, "type": true, "pub": true, "import": true,
	"as": true, "break": true, "continue": true, "true": true, "false": true,
}

var fernTypes = map[string]bool{
	"i32": true, "i64": true, "f32": true, "f64": true, "u8": true,
	"bool": true, "boolean": true, "string": true, "void": true,
}

// highlightFern returns one source line as HTML-escaped text with
// keyword / type / string / comment / number spans. It is a robust
// single-pass scan (not a real lexer): unknown shapes fall through as
// escaped plain text, so it never produces invalid HTML.
func highlightFern(line string) string {
	var b strings.Builder
	i, n := 0, len(line)
	isWord := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	for i < n {
		c := line[i]
		switch {
		case c == '/' && i+1 < n && line[i+1] == '/':
			fmt.Fprintf(&b, `<span class="c">%s</span>`, htmlEscape(line[i:]))
			i = n
		case c == '"':
			j := i + 1
			for j < n && line[j] != '"' {
				if line[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			if j < n {
				j++ // include closing quote
			}
			fmt.Fprintf(&b, `<span class="s">%s</span>`, htmlEscape(line[i:j]))
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < n && (isWord(line[j]) || line[j] == '.') {
				j++
			}
			fmt.Fprintf(&b, `<span class="n">%s</span>`, htmlEscape(line[i:j]))
			i = j
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < n && isWord(line[j]) {
				j++
			}
			word := line[i:j]
			switch {
			case fernKeywords[word]:
				fmt.Fprintf(&b, `<span class="k">%s</span>`, word)
			case fernTypes[word]:
				fmt.Fprintf(&b, `<span class="t">%s</span>`, word)
			default:
				b.WriteString(htmlEscape(word))
			}
			i = j
		default:
			b.WriteString(htmlEscape(string(c)))
			i++
		}
	}
	return b.String()
}

const weaveCSS = `:root{--fg:#24292f;--muted:#57606a;--bg:#fff;--code-bg:#f6f8fa;--border:#d0d7de;--accent:#0969da;--kw:#cf222e;--type:#953800;--str:#0a3069;--com:#6e7781;--num:#0550ae}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif}
main{max-width:52rem;margin:0 auto;padding:2.5rem 1.25rem 6rem}
h1,h2,h3,h4,h5,h6{line-height:1.25;margin:1.6em 0 .6em;font-weight:600}
h1{font-size:1.9rem;border-bottom:1px solid var(--border);padding-bottom:.3em}
h2{font-size:1.5rem;border-bottom:1px solid var(--border);padding-bottom:.3em}
p{margin:.8em 0}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
blockquote{margin:.8em 0;padding:0 1em;color:var(--muted);border-left:.25em solid var(--border)}
hr{border:0;border-top:1px solid var(--border);margin:1.6em 0}
ul,ol{margin:.8em 0;padding-left:1.6em}
code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.9em;background:var(--code-bg);padding:.15em .35em;border-radius:6px}
pre.code{background:var(--code-bg);border:1px solid var(--border);border-radius:8px;padding:.85rem 1rem;overflow:auto;margin:0}
pre.code code{background:none;padding:0;font-size:.86rem;line-height:1.55}
section.chunk{margin:1.6em 0}
.chunk-label{font-weight:600;color:var(--muted);margin-bottom:.4em;scroll-margin-top:1rem}
.chunk-name{color:var(--fg)}
section.file .chunk-name code{background:none;padding:0;color:var(--fg)}
.xref{font-size:.85rem;color:var(--muted);margin:.4em 0 0;font-style:italic}
a.ref{font-weight:600}
.code .k{color:var(--kw)}
.code .t{color:var(--type)}
.code .s{color:var(--str)}
.code .c{color:var(--com);font-style:italic}
.code .n{color:var(--num)}
nav.toc{background:var(--code-bg);border:1px solid var(--border);border-radius:8px;padding:.8rem 1rem;margin:0 0 2rem}
nav.toc .toc-title{font-weight:600;font-size:.85rem;text-transform:uppercase;letter-spacing:.04em;color:var(--muted);margin-bottom:.4em}
nav.toc ul{list-style:none;margin:0;padding:0}
nav.toc li{margin:.15em 0}
nav.toc .toc-l2{padding-left:1rem}
nav.toc .toc-l3{padding-left:2rem}
nav.toc .toc-l4,nav.toc .toc-l5,nav.toc .toc-l6{padding-left:3rem}
section.chunk-index{margin-top:3rem;border-top:1px solid var(--border);padding-top:1rem}
section.chunk-index ul{list-style:none;margin:0;padding:0}
section.chunk-index li{margin:.25em 0;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.9rem}
.idx-note{color:var(--muted);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:.82rem}
`
