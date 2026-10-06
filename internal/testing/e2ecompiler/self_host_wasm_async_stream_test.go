package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

// asyncStreamProvider exports test:dep/s. prod / prodi / prodf return the
// readable end of a stream through task.return, then write into it: three
// u8s, forty s32s (1..40, more than the consumer's first buffer holds) and
// three f64s. sink / sinki read a stream to EOF and return the sum of its
// elements. Every write and read is async and waits when it is blocked.
const asyncStreamProvider = `(component
  (type $su8 (stream u8))
  (type $ss32 (stream s32))
  (type $sf64 (stream f64))
  (core module $mm (memory (export "mem") 1)
    (data (i32.const 16) "\0a\14\0c")
    (data (i32.const 24) "\00\00\00\00\00\00\f8\3f\00\00\00\00\00\00\04\40\00\00\00\00\00\00\43\40"))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "mem" (core memory $mem))
  (core func $new_u8 (canon stream.new $su8))
  (core func $new_s32 (canon stream.new $ss32))
  (core func $new_f64 (canon stream.new $sf64))
  (core func $write_u8 (canon stream.write $su8 async (memory $mem)))
  (core func $write_s32 (canon stream.write $ss32 async (memory $mem)))
  (core func $write_f64 (canon stream.write $sf64 async (memory $mem)))
  (core func $read_u8 (canon stream.read $su8 async (memory $mem)))
  (core func $read_s32 (canon stream.read $ss32 async (memory $mem)))
  (core func $dropw_u8 (canon stream.drop-writable $su8))
  (core func $dropw_s32 (canon stream.drop-writable $ss32))
  (core func $dropw_f64 (canon stream.drop-writable $sf64))
  (core func $dropr_u8 (canon stream.drop-readable $su8))
  (core func $dropr_s32 (canon stream.drop-readable $ss32))
  (core func $tr_u8 (canon task.return (result $su8)))
  (core func $tr_s32 (canon task.return (result $ss32)))
  (core func $tr_f64 (canon task.return (result $sf64)))
  (core func $tr_n (canon task.return (result s32)))
  (core func $wsnew (canon waitable-set.new))
  (core func $join (canon waitable.join))
  (core func $wait (canon waitable-set.wait (memory $mem)))
  (core module $m
    (import "env" "mem" (memory 1))
    (import "" "new-u8" (func $new_u8 (result i64)))
    (import "" "new-s32" (func $new_s32 (result i64)))
    (import "" "new-f64" (func $new_f64 (result i64)))
    (import "" "write-u8" (func $write_u8 (param i32 i32 i32) (result i32)))
    (import "" "write-s32" (func $write_s32 (param i32 i32 i32) (result i32)))
    (import "" "write-f64" (func $write_f64 (param i32 i32 i32) (result i32)))
    (import "" "read-u8" (func $read_u8 (param i32 i32 i32) (result i32)))
    (import "" "read-s32" (func $read_s32 (param i32 i32 i32) (result i32)))
    (import "" "dropw-u8" (func $dropw_u8 (param i32)))
    (import "" "dropw-s32" (func $dropw_s32 (param i32)))
    (import "" "dropw-f64" (func $dropw_f64 (param i32)))
    (import "" "dropr-u8" (func $dropr_u8 (param i32)))
    (import "" "dropr-s32" (func $dropr_s32 (param i32)))
    (import "" "tr-u8" (func $tr_u8 (param i32)))
    (import "" "tr-s32" (func $tr_s32 (param i32)))
    (import "" "tr-f64" (func $tr_f64 (param i32)))
    (import "" "tr-n" (func $tr_n (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "join" (func $join (param i32 i32)))
    (import "" "wait" (func $wait (param i32 i32) (result i32)))
    ;; settle waits for handle h's op when status st is BLOCKED and returns
    ;; its completion, from the event the wait writes at 8.
    (func $settle (param $h i32) (param $st i32) (result i32) (local $ws i32)
      (if (i32.ne (local.get $st) (i32.const -1)) (then (return (local.get $st))))
      (local.set $ws (call $wsnew))
      (call $join (local.get $h) (local.get $ws))
      (drop (call $wait (local.get $ws) (i32.const 8)))
      (call $join (local.get $h) (i32.const 0))
      (i32.load (i32.const 12)))
    (func (export "prod") (local $h i64) (local $w i32)
      (local.set $h (call $new_u8))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $h) (i64.const 32))))
      (call $tr_u8 (i32.wrap_i64 (local.get $h)))
      (drop (call $settle (local.get $w) (call $write_u8 (local.get $w) (i32.const 16) (i32.const 3))))
      (call $dropw_u8 (local.get $w)))
    (func (export "prodi") (local $h i64) (local $w i32) (local $i i32) (local $done i32)
      (block $d (loop $l
        (br_if $d (i32.ge_u (local.get $i) (i32.const 40)))
        (i32.store (i32.add (i32.const 256) (i32.mul (local.get $i) (i32.const 4))) (i32.add (local.get $i) (i32.const 1)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (local.set $h (call $new_s32))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $h) (i64.const 32))))
      (call $tr_s32 (i32.wrap_i64 (local.get $h)))
      (block $d (loop $l
        (br_if $d (i32.ge_u (local.get $done) (i32.const 40)))
        (local.set $done (i32.add (local.get $done) (i32.shr_u
          (call $settle (local.get $w) (call $write_s32 (local.get $w)
            (i32.add (i32.const 256) (i32.mul (local.get $done) (i32.const 4)))
            (i32.sub (i32.const 40) (local.get $done))))
          (i32.const 4))))
        (br $l)))
      (call $dropw_s32 (local.get $w)))
    (func (export "prodf") (local $h i64) (local $w i32)
      (local.set $h (call $new_f64))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $h) (i64.const 32))))
      (call $tr_f64 (i32.wrap_i64 (local.get $h)))
      (drop (call $settle (local.get $w) (call $write_f64 (local.get $w) (i32.const 24) (i32.const 3))))
      (call $dropw_f64 (local.get $w)))
    (func (export "sink") (param $r i32) (local $st i32) (local $n i32) (local $i i32) (local $t i32)
      (block $d (loop $l
        (local.set $st (call $settle (local.get $r) (call $read_u8 (local.get $r) (i32.const 1024) (i32.const 2))))
        (local.set $n (i32.shr_u (local.get $st) (i32.const 4)))
        (local.set $i (i32.const 0))
        (block $sd (loop $sl
          (br_if $sd (i32.ge_u (local.get $i) (local.get $n)))
          (local.set $t (i32.add (local.get $t) (i32.load8_u (i32.add (i32.const 1024) (local.get $i)))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $sl)))
        (br_if $d (i32.and (local.get $st) (i32.const 15)))
        (br $l)))
      (call $dropr_u8 (local.get $r))
      (call $tr_n (local.get $t)))
    (func (export "sinki") (param $r i32) (local $st i32) (local $n i32) (local $i i32) (local $t i32)
      (block $d (loop $l
        (local.set $st (call $settle (local.get $r) (call $read_s32 (local.get $r) (i32.const 2048) (i32.const 2))))
        (local.set $n (i32.shr_u (local.get $st) (i32.const 4)))
        (local.set $i (i32.const 0))
        (block $sd (loop $sl
          (br_if $sd (i32.ge_u (local.get $i) (local.get $n)))
          (local.set $t (i32.add (local.get $t) (i32.load (i32.add (i32.const 2048) (i32.mul (local.get $i) (i32.const 4))))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $sl)))
        (br_if $d (i32.and (local.get $st) (i32.const 15)))
        (br $l)))
      (call $dropr_s32 (local.get $r))
      (call $tr_n (local.get $t))))
  (core instance $env (export "mem" (memory $mem)))
  (core instance $lib
    (export "new-u8" (func $new_u8)) (export "new-s32" (func $new_s32)) (export "new-f64" (func $new_f64))
    (export "write-u8" (func $write_u8)) (export "write-s32" (func $write_s32)) (export "write-f64" (func $write_f64))
    (export "read-u8" (func $read_u8)) (export "read-s32" (func $read_s32))
    (export "dropw-u8" (func $dropw_u8)) (export "dropw-s32" (func $dropw_s32)) (export "dropw-f64" (func $dropw_f64))
    (export "dropr-u8" (func $dropr_u8)) (export "dropr-s32" (func $dropr_s32))
    (export "tr-u8" (func $tr_u8)) (export "tr-s32" (func $tr_s32)) (export "tr-f64" (func $tr_f64)) (export "tr-n" (func $tr_n))
    (export "wsnew" (func $wsnew)) (export "join" (func $join)) (export "wait" (func $wait)))
  (core instance $i (instantiate $m (with "env" (instance $env)) (with "" (instance $lib))))
  (type $prod_t (func async (result $su8)))
  (type $prodi_t (func async (result $ss32)))
  (type $prodf_t (func async (result $sf64)))
  (type $sink_t (func async (param "s" $su8) (result s32)))
  (type $sinki_t (func async (param "s" $ss32) (result s32)))
  (func $prod (type $prod_t) (canon lift (core func $i "prod") async))
  (func $prodi (type $prodi_t) (canon lift (core func $i "prodi") async))
  (func $prodf (type $prodf_t) (canon lift (core func $i "prodf") async))
  (func $sink (type $sink_t) (canon lift (core func $i "sink") async))
  (func $sinki (type $sinki_t) (canon lift (core func $i "sinki") async))
  (instance $x (export "prod" (func $prod)) (export "prodi" (func $prodi)) (export "prodf" (func $prodf))
    (export "sink" (func $sink)) (export "sinki" (func $sinki)))
  (export "test:dep/s" (instance $x)))
`

// TestSelfHostWasmAsyncStream pins `stream[T]` in an async import's
// signature. A stream result is read to EOF into the T[] the call returns,
// and a T[] argument is written out through a stream the import reads. The
// provider writes and reads two elements at a time, so both sides wait on
// a blocked op, and prodi's forty elements make the consumer's buffer grow.
func TestSelfHostWasmAsyncStream(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	consumer, printed := buildAsyncComponent(t, cli, dir, `@import("test:dep/s", "prod") async function body(): stream[u8];
@import("test:dep/s", "prodi") async function nums(): stream[i32];
@import("test:dep/s", "prodf") async function reals(): stream[f64];
@import("test:dep/s", "sink") async function sink(s: stream[u8]): i32;
@import("test:dep/s", "sinki") async function sinki(s: stream[i32]): i32;
async function t_bytes(): i32 { let b: u8[] = body(); return (b[0] as i32) + (b[1] as i32) + (b[2] as i32); }
async function t_count(): i32 { let xs: i32[] = nums(); return xs.len(); }
async function t_sum(): i32 { let sum: i32 = 0; for x in nums() { sum = sum + x; } return sum; }
async function t_reals(): i32 { let sum: f64 = 0.0; for x in reals() { sum = sum + x; } return sum as i32; }
async function t_sink(): i32 { let xs: u8[] = [10 as u8, 20 as u8, 12 as u8]; return sink(xs); }
async function t_sinki(): i32 { let xs: i32[] = [100, 200, 300, 400, -958]; return sinki(xs); }
function main(): i32 { return 0; }
`)
	checkDeclares(t, printed, []string{`(canon stream.read`, `(canon stream.write`, `(canon stream.new`}, nil)
	linked := linkAsyncProvider(t, dir, consumer, asyncStreamProvider)
	for _, c := range []struct{ invoke, want string }{
		{"t-bytes()", "42"},
		{"t-count()", "40"},
		{"t-sum()", "820"},
		{"t-reals()", "42"},
		{"t-sink()", "42"},
		{"t-sinki()", "42"},
	} {
		args := append(append([]string{"run"}, asyncFeatures...), "--invoke", c.invoke, linked)
		out, err := exec.Command("wasmtime", args...).CombinedOutput()
		if err != nil {
			t.Errorf("--invoke %s: %v\n%s", c.invoke, err, out)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("--invoke %s = %q, want %q", c.invoke, got, c.want)
		}
	}
}
