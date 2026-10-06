package e2ecompiler

// strProbeHelpers are the byte-loop prefix, suffix and substring checks the
// RC probes assert buffer contents with. They allocate nothing, so a check
// inside a measured loop leaves the heap figures alone. The single-program
// drivers load no imports, so std/string's methods are out of reach there.
const strProbeHelpers = `function has_prefix(s: string, p: string): boolean { let lp: i32 = p.len(); if (lp > s.len()) { return false; } let i: i32 = 0; while (i < lp) { if (s[i] != p[i]) { return false; } i = i + 1; } return true; }
function has_suffix(s: string, p: string): boolean { let ls: i32 = s.len(); let lp: i32 = p.len(); if (lp > ls) { return false; } let off: i32 = ls - lp; let i: i32 = 0; while (i < lp) { if (s[off + i] != p[i]) { return false; } i = i + 1; } return true; }
function has_sub(s: string, sub: string): boolean { let ls: i32 = s.len(); let ln: i32 = sub.len(); if (ln > ls) { return false; } let last: i32 = ls - ln; let i: i32 = 0; while (i <= last) { let j: i32 = 0; let ok: boolean = true; while (j < ln) { if (s[i + j] != sub[j]) { ok = false; j = ln; } else { j = j + 1; } } if (ok) { return true; } i = i + 1; } return false; }
`
