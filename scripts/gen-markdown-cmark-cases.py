#!/usr/bin/env python3
"""Generate examples/markdown/tests/cmark.json: the command lines of the
cases below, and what cmark 0.31.2 gives for each of them on stdout and
stderr, with its exit status. TestSelfHostExampleMarkdownCmarkCases runs
`md` on each case on each target and compares the result."""
import base64
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'examples/markdown/tests/cmark.json'

# The files that a case can name. The names are relative to the directory
# where the command runs. `dir` is a directory.
FILES = {
    'a.md': b'# A\n\nsome *text',
    'b.md': b' continued\n\n- x\n',
    'bom.md': b'\xef\xbb\xbf# bom\r\n\r',
    'quote.md': b'\n> q\n',
    'bad.md': b'a\xffb \xe2\x82 \xf0\x9f\x98 \xed\xa0\x80 \xc0\xaf \xf4\x90\x80\x80 ok\n\xe2A\n*x\xf0*\n\xef\xbf\xbe\n',
    'nul.md': b'a\x00b\n\x00\n',
}

# Documents with bytes that are not UTF-8 in each place where the parser or a
# renderer reads them. cmark copies the bytes as they are, and with
# --validate-utf8 it replaces them as it reads each line.
INVALID = {
    'bad-text.md': b'a\xffb c\xe2\x82 d\xf0\x9f\x98 e\xed\xa0\x80 f\xc0\xaf g\xf4\x90\x80\x80 h\xf5\x80\x80\x80 i\xe0\x80\x80 j\x80k\n'
                   b'\xe2A\n\xc3\xa9\x80\n\xef\xbf\xbe \xef\xbf\xbf\n',
    'bad-labels.md': b'[a\xffb] [A\xfeB] [\xe2\x82] [\xc3\x89\xff]\n\n[a\xfeb]: /u1\n[a\xfdb]: /u2\n[\xe2\x82]: /u3 "t\xff"\n[\xc3\xa9\xfe]: /u4\n',
    'bad-code.md': b'`a\xffb` `` \xe2\x82 `` ` \xff `\n\n    co\xffde\n\n```in\xfffo x\xfe\nc\xffd\n```\n\n~~~ \xe2\x82\n~~~\n',
    'bad-html.md': b'<div>\na\xffb\n</div>\n\n<!-- \xfe -->\n\n<a href="\xff">x</a> <b\xff> <x y="\xfe">\n\n<pre>\n\xff\n</pre>\n',
    'bad-entities.md': b'&\xff; &amp\xff; &#\xff; &#x\xfe; &copy;\xff &\xe2\x82; x&#65;\xffy\n\n'
                       b'[l](/a&amp;\xff "t&quot;\xfe") <http://a\xff.b/&amp;> <a\xff@b.c>\n',
    'bad-links.md': b'[t\xffx](/d\xffe "ti\xfetle") ![i\xffm](/s\xfe) [x](<\xff y>) [y](/\xe2\x82)\n',
    'bad-delims.md': b'\xff*a*\xff \xe2\x82*b* *c*\xe2\x82 \xc3\xa9\x80*d* _e_\xff \xff_f_ **\xffg\xff**\n'
                     b'\xff"a"\xff \xe2\x82\'b\' "\xffc\xff" \xff-- \xff... \xff---\n',
    'bad-blocks.md': b'# H\xff #\n\nS\xff\n===\n\n> q\xffq\n> > \xfe\n\n- \xffa\n- b\xfe\n\n1. \xe2\x82\n\n\xff\n\n---\xff\n',
    'bad-wrap.md': b'aaa bbb \xffccc ddd eee fff ggg\n\n> aaa bbb ccc \xe2\x82 ddd eee\n\n- x yy zzz \xff www vvv uuu\n',
    'bad-lines.md': b'a\x00\xff\x00b\n\na\xff\r\nb\xfe\rc\xe2\x82\r\n',
    'bad-end1.md': b'text at the end \xe2\x82',
    'bad-end2.md': b'# heading \xff',
    'bad-end3.md': b'*\xff',
    'bad-end4.md': b'> quote\n> \xff',
}
FILES.update(INVALID)

STDIN = b'stdin *text*\n<b>x</b>\n'

# A document with each kind of block and inline, for the formats.
DOC = (b'# Title *em*\n\nSetext `code`\n---\n\n> quote "with" \'quotes\'\n> and a [link](/u "title") and <http://a.b>\n\n'
       b'- one\n- two\n\n  para -- dash --- and ...\n\n3) three\n4) four\n\n```py x\nprint(1)\n```\n\n    indented\n\n'
       b'<div>\nraw\n</div>\n\n***\n\nHard  \nbreak\\\nsoft\nbreak ![img *a*](/i.png \'t\') <b>tag</b> &amp; &copy; <a@b.c>\n\n'
       b'[ref]: /r "R"\n\n[ref] [x][ref] 1. 2) + = # !\n')

SMART = (b'"Hello," she said. \'It\'s "quoted"\' -- yes --- no ---- ----- ...\n'
         b'\'a\' "b" ("c") ["d"] *"e"* 5\'6" \'\' "\'nested\'" \\"escaped\\"\n'
         b'[a "link"](/u "t\'s") ![x\'s](/i) `no \'smart\' -- here` <b title="x">\n'
         b'"*a*" \'_b_\' "**c**"... a.. b.... c-d e--f g---h -\n')

POSITIONS = (b'  # h #  \n\n> - a\n>\n>   b `multi\n> line` <span\n> x="y">\n\n1. x\n\n```\nc\n```\n\n    i\n\n---\n\nSetext\n===\n\n'
             b'[r]: /u\nfoo *bar\\\nbaz* [l](/u\n"t") <http://x.y>\n\n\t\ttab\n- \tz\n\n<div>\n')

WRAP = (b'> aaa bbb ccc ddd eee fff ggg hhh 1 2 3 4\n\n- iii jjj kkk lll mmm nnn ooo ppp\n  1. nested qqq rrr sss ttt\n\n'
        b'A line with `code span text` and *emphasis words* and [a link](http://example.com/long/path) that wraps.\n'
        b'Numbers 10. 11) at - wrap + points = ok.\n')


def cases():
    out = []

    def add(args, stdin=STDIN, files=()):
        out.append({'args': args, 'stdin': stdin, 'files': list(files)})

    # The options, and the errors that end a command line.
    for args in (['--help'], ['-h'], ['--version'], ['--bogus'], ['-'], ['-x', 'a.md'], ['--width'], ['--width', 'abc'],
                 ['--width', '12abc'], ['--width', ''], ['--width', ' 5', '-t', 'commonmark', 'b.md'],
                 ['--width', '-3', '-t', 'commonmark'], ['--width', '99999999999999999999', '-t', 'commonmark'],
                 ['--width', '+7', '-t', 'man'], ['-t'], ['--to'], ['-t', 'foo'], ['-t', 'HTML'], ['--to', 'xml', 'a.md'],
                 ['--bogus', '--version'], ['-h', '--bogus'], ['--width', '5', '--width'], [''],
                 ['missing.md', '--bogus'], ['--safe'], ['--safe', '--unsafe'], ['--unsafe', '--safe']):
        add(args, files=FILES)
    # Files: concatenation, a file that is not there, a directory.
    for args in (['a.md', 'b.md'], ['a.md', 'missing.md', 'b.md'], ['dir'], ['dir', 'a.md'], ['bom.md', 'quote.md'],
                 ['quote.md', 'bom.md'], ['a.md', '-t', 'latex', 'b.md'], ['--sourcepos', 'bom.md', 'a.md'],
                 ['nul.md'], ['--sourcepos', '-t', 'xml', 'nul.md']):
        add(args, files=FILES)
    # Invalid UTF-8, as cmark copies it and as --validate-utf8 replaces it.
    for fmt in ('html', 'xml', 'man', 'latex', 'commonmark'):
        for name in ['bad.md'] + list(INVALID):
            add(['-t', fmt, name], files=FILES)
            add(['--validate-utf8', '-t', fmt, name], files=FILES)
    for opts in (['--smart'], ['--sourcepos', '--unsafe'], ['-t', 'xml', '--sourcepos', '--smart'],
                 ['-t', 'commonmark', '--width', '12'], ['-t', 'man', '--width', '12'], ['-t', 'latex', '--width', '12', '--smart']):
        for name in ('bad-delims.md', 'bad-wrap.md', 'bad-blocks.md', 'bad-html.md'):
            add(opts + [name], files=FILES)
    # Each format with each option.
    for fmt in ('html', 'xml', 'man', 'latex', 'commonmark'):
        for opts in ([], ['--unsafe'], ['--smart'], ['--hardbreaks'], ['--nobreaks'], ['--sourcepos'],
                     ['--sourcepos', '--smart', '--unsafe'], ['--width', '30']):
            add(['-t', fmt] + opts, stdin=DOC)
        for opts in ([], ['--width', '20']):
            add(['-t', fmt, '--smart'] + opts, stdin=SMART)
        add(['-t', fmt, '--sourcepos'], stdin=POSITIONS)
    for fmt in ('man', 'latex', 'commonmark'):
        for width in ('1', '8', '20', '40'):
            add(['-t', fmt, '--width', width], stdin=WRAP)
        add(['-t', fmt, '--width', '20', '--nobreaks'], stdin=WRAP)
        add(['-t', fmt, '--width', '20', '--hardbreaks'], stdin=WRAP)
    return out


def text(b):
    """A string when the bytes are UTF-8, and else their base64 in an object."""
    try:
        return b.decode('utf-8')
    except UnicodeDecodeError:
        return {'base64': base64.b64encode(b).decode('ascii')}


def main():
    cmark = shutil.which('cmark')
    if cmark is None:
        sys.exit('cmark 0.31.2 is not on PATH')
    version = subprocess.run([cmark, '--version'], capture_output=True).stdout
    if not version.startswith(b'cmark 0.31.2 '):
        sys.exit('the oracle is cmark 0.31.2, not %r' % version)
    rows = []
    with tempfile.TemporaryDirectory() as tmp:
        for name, data in FILES.items():
            (Path(tmp) / name).write_bytes(data)
        os.mkdir(Path(tmp) / 'dir')
        for c in cases():
            run = subprocess.run([cmark] + c['args'], input=c['stdin'], capture_output=True, cwd=tmp)
            rows.append({'args': c['args'], 'stdin': text(c['stdin']), 'stdout': text(run.stdout),
                         'stderr': text(run.stderr), 'exit': run.returncode})
    files = {name: text(data) for name, data in FILES.items()}
    OUT.write_text(json.dumps({'files': files, 'cases': rows}, ensure_ascii=False, indent=1) + '\n')
    print('%s: %d cases' % (OUT.relative_to(ROOT), len(rows)))


if __name__ == '__main__':
    main()
