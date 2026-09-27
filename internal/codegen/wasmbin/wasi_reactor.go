// The reactor floor (#9853) for the wasmbin backend: a readiness set kept
// between waits over wasi:io/poll.
//
// A reactor is a 12-byte header {n @0, cap @4, entries @8} whose entries
// buffer holds 16-byte rows {socket record @0, interest @4, read pollable
// @8, write pollable @12}. Watching a record subscribes the pollables its
// kind word names (wasi_tcp.go): a listener or a connect under way through
// tcp-socket.subscribe, a connection through its input and output streams,
// a datagram socket through its datagram streams. A wait polls every
// pollable the interests select plus a timer for a finite timeout, and
// writes (record, readiness) pairs for the ready ones into the caller's
// i32[]. Pollables outlive one poll, so a watch subscribes once and a
// change of interest or an unwatch drops what it held.
//
//	reactor_new   () → header, or -errno
//	reactor_ctl   (r, op, fd, arg) → 0 or -errno: op 1 watch fd for the
//	              interest in arg (1 readable, 2 writable), 2 unwatch, 3 close
//	reactor_wait  (r, events, timeout_ms) → the pair count, 0 on the
//	              timeout, or -errno
//	tcp_recv_into (rec, buf) → bytes read into the u8[] at buf, 0 at EOF,
//	              or -errno

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// Preview 1 errno numbers the reactor answers before any host call.
const (
	errnoReactorBadf  = 8
	errnoReactorInval = 28
	errnoReactorIo    = 29
	errnoReactorAgain = 6
)

const reactorEntrySize = 16

// reactorEmit is the small vocabulary the builders share, over one body.
type reactorEmit struct {
	body []byte
	idxs map[string]uint32
}

func (w *reactorEmit) lg(i uint32)   { w.body = inst.InstLocalGet(w.body, i) }
func (w *reactorEmit) ls(i uint32)   { w.body = inst.InstLocalSet(w.body, i) }
func (w *reactorEmit) k(v int32)     { w.body = inst.InstI32Const(w.body, v) }
func (w *reactorEmit) call(n string) { w.body = inst.InstCall(w.body, w.idxs[n]) }
func (w *reactorEmit) ld(off uint32) { w.body = memory.InstI32Load(w.body, 2, off) }
func (w *reactorEmit) st(off uint32) { w.body = memory.InstI32Store(w.body, 2, off) }
func (w *reactorEmit) add()          { w.body = numeric.InstI32Add(w.body) }
func (w *reactorEmit) sub()          { w.body = numeric.InstI32Sub(w.body) }
func (w *reactorEmit) mul()          { w.body = numeric.InstI32Mul(w.body) }
func (w *reactorEmit) and()          { w.body = numeric.InstI32And(w.body) }
func (w *reactorEmit) eq()           { w.body = numeric.InstI32Eq(w.body) }
func (w *reactorEmit) ne()           { w.body = numeric.InstI32Ne(w.body) }
func (w *reactorEmit) ifStart()      { w.body = inst.InstIfStart(w.body, inst.BlocktypeEmpty) }
func (w *reactorEmit) elseStart()    { w.body = inst.InstElse(w.body) }
func (w *reactorEmit) end()          { w.body = inst.InstEnd(w.body) }
func (w *reactorEmit) ret()          { w.body = inst.InstReturn(w.body) }

// retConst returns the constant from the function.
func (w *reactorEmit) retConst(v int32) { w.k(v); w.ret() }

// entryAt leaves entries + i*16 on the stack.
func (w *reactorEmit) entryAt(ents, i uint32) {
	w.lg(ents)
	w.lg(i)
	w.k(reactorEntrySize)
	w.mul()
	w.add()
}

// loop runs body for i in [0, n): the caller's body must not branch out.
func (w *reactorEmit) loop(i, n uint32, body func()) {
	w.k(0)
	w.ls(i)
	w.body = inst.InstBlockStart(w.body, inst.BlocktypeEmpty)
	w.body = inst.InstLoopStart(w.body, inst.BlocktypeEmpty)
	w.lg(i)
	w.lg(n)
	w.body = numeric.InstI32GeU(w.body)
	w.body = inst.InstBrIf(w.body, 1)
	body()
	w.lg(i)
	w.k(1)
	w.add()
	w.ls(i)
	w.body = inst.InstBr(w.body, 0)
	w.end()
	w.end()
}

// free(ptr, size) with both from locals or constants already emitted by
// the caller.
func (w *reactorEmit) free() { w.call("__free") }

// dropPollables drops the pollables the entry in local e holds, per its
// interest bits.
func (w *reactorEmit) dropPollables(e uint32) {
	for _, bit := range []struct {
		mask int32
		off  uint32
	}{{1, 8}, {2, 12}} {
		w.lg(e)
		w.ld(4)
		w.k(bit.mask)
		w.and()
		w.ifStart()
		w.lg(e)
		w.ld(bit.off)
		w.call("wasi_io_pollable_drop")
		w.end()
	}
}

// subscribe fills the entry in local e with the pollables for the record
// in local fd and the interest in local interest, by the record's kind
// word; a kind with no streams to subscribe leaves the interest at zero
// and returns -EINVAL. Local kind is scratch.
func (w *reactorEmit) subscribe(e, fd, interest, kind uint32) {
	w.lg(fd)
	w.ld(12)
	w.ls(kind)
	// kind 1: a connection's streams.
	pair := func(kindWant int32, readSub, writeSub string, readOff, writeOff uint32) {
		w.lg(kind)
		w.k(kindWant)
		w.eq()
		w.ifStart()
		for _, half := range []struct {
			mask   int32
			sub    string
			srcOff uint32
			dstOff uint32
		}{{1, readSub, readOff, 8}, {2, writeSub, writeOff, 12}} {
			w.lg(interest)
			w.k(half.mask)
			w.and()
			w.ifStart()
			w.lg(e)
			w.lg(fd)
			w.ld(half.srcOff)
			w.call(half.sub)
			w.st(half.dstOff)
			w.end()
		}
		w.lg(e)
		w.lg(interest)
		w.st(4)
		w.retConst(0)
		w.end()
	}
	pair(1, "wasi_io_input_stream_subscribe", "wasi_io_output_stream_subscribe", 4, 8)
	pair(3, "wasi_sockets_udp_incoming_subscribe", "wasi_sockets_udp_outgoing_subscribe", 4, 8)
	// A listener (0) or a connect under way (4): the socket's own pollable
	// for either direction.
	pair(0, "wasi_sockets_tcp_subscribe", "wasi_sockets_tcp_subscribe", 0, 0)
	pair(tcpRecordConnecting, "wasi_sockets_tcp_subscribe", "wasi_sockets_tcp_subscribe", 0, 0)
	w.lg(e)
	w.k(0)
	w.st(4)
	w.retConst(-errnoReactorInval)
}

// buildReactorNewBody assembles __fern_reactor_new: () → i32.
//
// Locals: 0 $hdr.
func buildReactorNewBody(idxs map[string]uint32) []byte {
	w := &reactorEmit{idxs: idxs}
	w.k(12)
	w.call("__fern_alloc")
	w.ls(0)
	w.lg(0)
	w.k(0)
	w.st(0)
	w.lg(0)
	w.k(16)
	w.st(4)
	w.lg(0)
	w.k(16 * reactorEntrySize)
	w.call("__fern_alloc")
	w.st(8)
	w.lg(0)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32), w.body)
}

// buildReactorCtlBody assembles __fern_reactor_ctl: (r, op, fd, arg) → i32.
//
// Locals (after the four params): 4 $n, 5 $ents, 6 $i, 7 $e, 8 $interest,
// 9 $kind, 10 $cap, 11 $grown.
func buildReactorCtlBody(idxs map[string]uint32) []byte {
	const (
		r, op, fd, arg                     = 0, 1, 2, 3
		n, ents, i, e, interest, kind, cap = 4, 5, 6, 7, 8, 9, 10
		grown                              = 11
	)
	w := &reactorEmit{idxs: idxs}
	w.lg(r)
	w.ld(0)
	w.ls(n)
	w.lg(r)
	w.ld(8)
	w.ls(ents)
	w.lg(r)
	w.ld(4)
	w.ls(cap)

	// op 3: drop everything and free the set.
	w.lg(op)
	w.k(3)
	w.eq()
	w.ifStart()
	w.loop(i, n, func() {
		w.entryAt(ents, i)
		w.ls(e)
		w.dropPollables(e)
	})
	w.lg(ents)
	w.lg(cap)
	w.k(reactorEntrySize)
	w.mul()
	w.free()
	w.lg(r)
	w.k(12)
	w.free()
	w.retConst(0)
	w.end()

	// Only ops 1 and 2 remain.
	w.lg(op)
	w.k(1)
	w.sub()
	w.k(2)
	w.body = numeric.InstI32GeU(w.body)
	w.ifStart()
	w.retConst(-errnoReactorInval)
	w.end()
	w.k(0)
	w.ls(interest)
	w.lg(op)
	w.k(1)
	w.eq()
	w.ifStart()
	w.lg(arg)
	w.k(3)
	w.and()
	w.ls(interest)
	w.end()

	// Find the record's entry.
	w.loop(i, n, func() {
		w.entryAt(ents, i)
		w.ls(e)
		w.lg(e)
		w.ld(0)
		w.lg(fd)
		w.eq()
		w.ifStart()
		w.dropPollables(e)
		w.lg(interest)
		w.body = numeric.InstI32Eqz(w.body)
		w.ifStart()
		// Unwatch: the last entry takes the slot.
		w.lg(e)
		w.entryAt(ents, n)
		w.k(reactorEntrySize)
		w.sub()
		w.k(reactorEntrySize)
		w.body = memory.InstMemoryCopy(w.body)
		w.lg(r)
		w.lg(n)
		w.k(1)
		w.sub()
		w.st(0)
		w.retConst(0)
		w.end()
		w.subscribe(e, fd, interest, kind)
		w.end()
	})

	// Not watched yet.
	w.lg(interest)
	w.body = numeric.InstI32Eqz(w.body)
	w.ifStart()
	w.retConst(0)
	w.end()
	w.lg(n)
	w.lg(cap)
	w.eq()
	w.ifStart()
	w.lg(cap)
	w.k(2 * reactorEntrySize)
	w.mul()
	w.call("__fern_alloc")
	w.ls(grown)
	w.lg(grown)
	w.lg(ents)
	w.lg(n)
	w.k(reactorEntrySize)
	w.mul()
	w.body = memory.InstMemoryCopy(w.body)
	w.lg(ents)
	w.lg(cap)
	w.k(reactorEntrySize)
	w.mul()
	w.free()
	w.lg(r)
	w.lg(grown)
	w.st(8)
	w.lg(r)
	w.lg(cap)
	w.k(2)
	w.mul()
	w.st(4)
	w.lg(grown)
	w.ls(ents)
	w.end()
	w.entryAt(ents, n)
	w.ls(e)
	w.lg(e)
	w.lg(fd)
	w.st(0)
	w.lg(r)
	w.lg(n)
	w.k(1)
	w.add()
	w.st(0)
	w.subscribe(e, fd, interest, kind)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32), w.body)
}

// buildReactorWaitBody assembles __fern_reactor_wait: (r, events, timeout_ms) → i32.
//
// The pollables the interests select go into a list for wasi:io/poll.poll,
// each with an (record, bit) pair in a parallel table; a finite timeout
// appends a monotonic-clock timer whose pair is (0, 0). The ready indices
// come back as a list in this heap (cabi_realloc), read and freed.
//
// Locals (after the three params): 3 $n, 4 $ents, 5 $i, 6 $e, 7 $count,
// 8 $list, 9 $slots, 10 $k, 11 $timer, 12 $ret, 13 $rl, 14 $rc, 15 $out,
// 16 $cap, 17 $idx, 18 $bits, 19 $j.
func buildReactorWaitBody(idxs map[string]uint32) []byte {
	const (
		r, events, timeout                               = 0, 1, 2
		n, ents, i, e, count, list, slots, k, timer, ret = 3, 4, 5, 6, 7, 8, 9, 10, 11, 12
		rl, rc, out, cap, idx, bits, j                   = 13, 14, 15, 16, 17, 18, 19
	)
	w := &reactorEmit{idxs: idxs}
	w.lg(events)
	w.k(4)
	w.sub()
	w.ld(0)
	w.k(2)
	w.body = numeric.InstI32DivU(w.body)
	w.ls(cap)
	w.lg(cap)
	w.body = numeric.InstI32Eqz(w.body)
	w.ifStart()
	w.retConst(-errnoReactorInval)
	w.end()
	w.lg(r)
	w.ld(0)
	w.ls(n)
	w.lg(r)
	w.ld(8)
	w.ls(ents)

	// count = the selected pollables, plus the timer.
	w.k(0)
	w.ls(count)
	w.loop(i, n, func() {
		w.entryAt(ents, i)
		w.ld(4)
		w.ls(bits)
		w.lg(count)
		w.lg(bits)
		w.k(1)
		w.and()
		w.add()
		w.lg(bits)
		w.k(1)
		w.body = numeric.InstI32ShrU(w.body)
		w.k(1)
		w.and()
		w.add()
		w.ls(count)
	})
	w.lg(timeout)
	w.k(0)
	w.body = numeric.InstI32GeS(w.body)
	w.ifStart()
	w.lg(count)
	w.k(1)
	w.add()
	w.ls(count)
	w.end()
	w.lg(count)
	w.body = numeric.InstI32Eqz(w.body)
	w.ifStart()
	w.retConst(-errnoReactorInval)
	w.end()

	w.lg(count)
	w.k(4)
	w.mul()
	w.call("__fern_alloc")
	w.ls(list)
	w.lg(count)
	w.k(8)
	w.mul()
	w.call("__fern_alloc")
	w.ls(slots)
	w.k(0)
	w.ls(k)
	// slot(pollable, record, bit): list[k] = pollable; slots[k] = (record, bit); k++.
	slot := func(pollable func(), record func(), bit int32) {
		w.lg(list)
		w.lg(k)
		w.k(4)
		w.mul()
		w.add()
		pollable()
		w.st(0)
		w.lg(slots)
		w.lg(k)
		w.k(8)
		w.mul()
		w.add()
		record()
		w.st(0)
		w.lg(slots)
		w.lg(k)
		w.k(8)
		w.mul()
		w.add()
		w.k(bit)
		w.st(4)
		w.lg(k)
		w.k(1)
		w.add()
		w.ls(k)
	}
	w.loop(i, n, func() {
		w.entryAt(ents, i)
		w.ls(e)
		for _, half := range []struct {
			mask int32
			off  uint32
		}{{1, 8}, {2, 12}} {
			w.lg(e)
			w.ld(4)
			w.k(half.mask)
			w.and()
			w.ifStart()
			slot(func() { w.lg(e); w.ld(half.off) }, func() { w.lg(e); w.ld(0) }, half.mask)
			w.end()
		}
	})
	w.lg(timeout)
	w.k(0)
	w.body = numeric.InstI32GeS(w.body)
	w.ifStart()
	w.lg(timeout)
	w.body = convert.InstI64ExtendI32S(w.body)
	w.body = inst.InstI64Const(w.body, 1000000)
	w.body = numeric.InstI64Mul(w.body)
	w.call("wasi_clocks_subscribe_duration")
	w.ls(timer)
	slot(func() { w.lg(timer) }, func() { w.k(0) }, 0)
	w.end()

	w.k(8)
	w.call("__fern_alloc")
	w.ls(ret)
	w.lg(list)
	w.lg(count)
	w.lg(ret)
	w.call("wasi_io_poll_poll")
	w.lg(ret)
	w.ld(0)
	w.ls(rl)
	w.lg(ret)
	w.ld(4)
	w.ls(rc)
	w.k(0)
	w.ls(out)
	w.loop(j, rc, func() {
		w.lg(rl)
		w.lg(j)
		w.k(4)
		w.mul()
		w.add()
		w.ld(0)
		w.ls(idx)
		w.lg(slots)
		w.lg(idx)
		w.k(8)
		w.mul()
		w.add()
		w.ld(4)
		w.ls(bits)
		w.lg(bits)
		w.ifStart()
		w.lg(out)
		w.lg(cap)
		w.body = numeric.InstI32LtU(w.body)
		w.ifStart()
		w.lg(events)
		w.lg(out)
		w.k(8)
		w.mul()
		w.add()
		w.lg(slots)
		w.lg(idx)
		w.k(8)
		w.mul()
		w.add()
		w.ld(0)
		w.st(0)
		w.lg(events)
		w.lg(out)
		w.k(8)
		w.mul()
		w.add()
		w.lg(bits)
		w.st(4)
		w.lg(out)
		w.k(1)
		w.add()
		w.ls(out)
		w.end()
		w.end()
	})
	w.lg(rc)
	w.ifStart()
	w.lg(rl)
	w.lg(rc)
	w.k(4)
	w.mul()
	w.free()
	w.end()
	w.lg(ret)
	w.k(8)
	w.free()
	w.lg(timeout)
	w.k(0)
	w.body = numeric.InstI32GeS(w.body)
	w.ifStart()
	w.lg(timer)
	w.call("wasi_io_pollable_drop")
	w.end()
	w.lg(list)
	w.lg(count)
	w.k(4)
	w.mul()
	w.free()
	w.lg(slots)
	w.lg(count)
	w.k(8)
	w.mul()
	w.free()
	w.lg(out)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 17, encode.ValtypeI32), w.body)
}

// buildTcpRecvIntoBody assembles __fern_tcp_recv_into: (rec, buf) → i32.
//
// One non-blocking read of up to the u8[]'s length (at buf-4) into it: a
// wasm socket never blocks a read, so nothing queued is -EAGAIN, as a
// native non-blocking socket answers. A closed stream is 0, a stream
// error -EIO with the error dropped, a record that is not a connection
// -EBADF.
//
// Locals (after the two params): 2 $ret, 3 $len, 4 $lp, 5 $ll.
func buildTcpRecvIntoBody(idxs map[string]uint32) []byte {
	const rec, buf, ret, ln, lp, ll = 0, 1, 2, 3, 4, 5
	w := &reactorEmit{idxs: idxs}
	w.lg(rec)
	w.ld(12)
	w.k(1)
	w.ne()
	w.ifStart()
	w.retConst(-errnoReactorBadf)
	w.end()
	w.lg(buf)
	w.k(4)
	w.sub()
	w.ld(0)
	w.ls(ln)
	w.lg(ln)
	w.body = numeric.InstI32Eqz(w.body)
	w.ifStart()
	w.retConst(0)
	w.end()
	w.k(12)
	w.call("__fern_alloc")
	w.ls(ret)
	w.lg(rec)
	w.ld(4)
	w.lg(ln)
	w.body = convert.InstI64ExtendI32U(w.body)
	w.lg(ret)
	w.call("wasi_io_stream_read")
	w.lg(ret)
	w.body = memory.InstI32Load8U(w.body, 0, 0)
	w.ifStart()
	// Err: closed (variant 1) is EOF; last-operation-failed (0) carries
	// an error resource to drop.
	w.lg(ret)
	w.body = memory.InstI32Load8U(w.body, 0, 4)
	w.ls(ll)
	w.body = emitStreamErrorDrop(w.body, idxs, ret)
	w.lg(ret)
	w.k(12)
	w.free()
	w.lg(ll)
	w.ifStart()
	w.retConst(0)
	w.end()
	w.retConst(-errnoReactorIo)
	w.end()
	w.lg(ret)
	w.ld(4)
	w.ls(lp)
	w.lg(ret)
	w.ld(8)
	w.ls(ll)
	// Nothing queued: the list is empty and the read would block.
	w.lg(ll)
	w.body = numeric.InstI32Eqz(w.body)
	w.ifStart()
	w.lg(ret)
	w.k(12)
	w.free()
	w.retConst(-errnoReactorAgain)
	w.end()
	w.lg(buf)
	w.lg(lp)
	w.lg(ll)
	w.body = memory.InstMemoryCopy(w.body)
	w.lg(ll)
	w.ifStart()
	w.lg(lp)
	w.lg(ll)
	w.free()
	w.end()
	w.lg(ret)
	w.k(12)
	w.free()
	w.lg(ll)
	return inst.PutFunctionBody(nil, inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32), w.body)
}
