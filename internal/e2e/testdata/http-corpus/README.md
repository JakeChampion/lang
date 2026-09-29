# The HTTP request parser corpus

The request fixtures of two other HTTP/1 parsers, each pinned to what
`std/http`'s parser makes of it, run by `TestHTTPCorpus` (`internal/e2e`) on
the interpreter and every compiled backend, and by `TestSelfHostHTTPCorpus`
(`internal/e2eselfhost`) through the self-host compiler.

One case per line, five tab-separated columns:

1. the name: the upstream project, its test file, and the fixture's title;
2. the upstream fixture's mode (`request`, or a `request-lenient-*` mode
   that llhttp runs with a leniency flag the corpus does not model);
3. what the upstream parser expects: `ok`, `error` or `partial`;
4. the wire bytes, hex-encoded;
5. `std/http`'s pinned verdict: `ok <method> <path> h=<headers> b=<body
   bytes> t=<trailers> len=<framed bytes> ka=<keep-alive>` (the path with
   every byte outside visible ASCII, and `%`, as `%XX`), `incomplete`,
   `continue`, or `malformed <status>`. A request refused with 400 as sent
   and answered otherwise once a `Host` header is put behind its request
   line reads `nohost <that answer>`: most upstream fixtures were written
   for parsers that do not enforce the `Host` rule, and the retry keeps
   that one rule from hiding every other answer.

The pin is `std/http`'s own rule: where it and the upstream parser disagree
(a lenient fixture accepted upstream and refused here, or a request llhttp
reads without a `Host`), both columns say so. Regenerate column 5 with
`FERN_HTTP_CORPUS_DUMP=1 go test ./internal/e2e -run TestHTTPCorpus`; a
changed pin is a parser change that has to be meant.

Sources, each fixture's text turned into wire bytes the way the upstream
harness does it (llhttp's `test/md-test.ts`: a lone LF becomes CRLF, `\r`,
`\n`, `\t`, `\f`, `\xHH` and octal escapes become their byte; httparse's
byte-string literals as Rust reads them):

- `llhttp.txt`: `test/request/*.md` of nodejs/llhttp at
  ee51043701d8b90ef647cf04790cbb1c55889825 (2026-07-30), MIT, © Fedor
  Indutny.
- `httparse.txt`: the `req!` cases of `src/lib.rs` and `tests/uri.rs` of
  seanmonstar/httparse at a0fa552e4e0fa9a5914a736ef3a556748fef709b
  (2026-06-30), MIT or Apache-2.0, © Sean McArthur.
- `smuggling.txt`: this repository's own request-smuggling cases (the
  CL.TE, TE.CL, TE.TE, 0.CL and `Expect` shapes of the CVE record, chunk
  framing tricks, header-line splits and request-target tricks). Column 3
  is what a server that cannot be desynchronised answers: `error` where
  the request must be refused, `ok` where one framing is the only reading
  and the bytes behind it are the next request's.
