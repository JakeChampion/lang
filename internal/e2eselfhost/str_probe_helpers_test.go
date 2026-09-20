package e2eselfhost

// strProbeHelpers are the byte-loop prefix, suffix and substring checks the
// RC probes assert buffer contents with. They allocate nothing, so a check
// inside a measured loop leaves the heap figures alone. The single-program
// drivers load no imports, so std/string's methods are out of reach there.
const strProbeHelpers = `function has_prefix(s: string, p: string): boolean { var lp: i32 = p.len(); if (lp > s.len()) { return false; } var i: i32 = 0; while (i < lp) { if (s[i] != p[i]) { return false; } i = i + 1; } return true; }
function has_suffix(s: string, p: string): boolean { var ls: i32 = s.len(); var lp: i32 = p.len(); if (lp > ls) { return false; } var off: i32 = ls - lp; var i: i32 = 0; while (i < lp) { if (s[off + i] != p[i]) { return false; } i = i + 1; } return true; }
function has_sub(s: string, sub: string): boolean { var ls: i32 = s.len(); var ln: i32 = sub.len(); if (ln > ls) { return false; } var last: i32 = ls - ln; var i: i32 = 0; while (i <= last) { var j: i32 = 0; var ok: boolean = true; while (j < ln) { if (s[i + j] != sub[j]) { ok = false; j = ln; } else { j = j + 1; } } if (ok) { return true; } i = i + 1; } return false; }
`
