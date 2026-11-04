# 2026-11-04 — a short memchr tail takes one vector compare

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

`ls` asks of each name whether it holds a newline. The question is
`s.contains("\n")`, which is one `__memchr` call. A name has about 12
bytes. The memchr kernel compares 32 bytes and then 16 bytes in a vector
loop. A name of fewer than 16 bytes thus went through the byte loop. Each
byte took six instructions.

## The rule

Fewer than 16 bytes remain at the tail. When the 16-byte load that
starts at the next byte stays in its page, one vector compare covers the
tail. The kernel takes the first matching lane and treats a lane at or
past the end as no match. When the load crosses into the next page, the
kernel takes the byte loop. Thus a guard page after a block is never
touched.

x86-64 compares with `pcmpeqb` and finds the lane with `bsf`. arm64
compares with `cmeq` and finds the lane with `rbit` and `clz`. The bytes
past the end are read, but they are never part of a result.

| `ls` of 3911 names, x86-64 instructions | before | after | GNU 9.4 |
|---|--:|--:|--:|
| whole run | 10,398,108 | 10,216,997 | 11,327,549 |

## Tests

`TestSelfHostMemchrIR` searches for byte 0 in each of its strings, at
each start. The memory after a string is mostly zero, so a kernel that
reports a match past the end fails with code 11. This is true on
x86-64 and on arm64: each backend fails the test without its end check.
`TestSelfHostCtGateX86_64` stays clean under memcheck.
