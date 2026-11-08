package checker

import "testing"

const readerReturnDecls = `struct State { n: i32 }
function (own s: State) step_with(read: (i32) => Result[u8[], IoError]): Result[(State, i32), string] {
    match (read(0)) {
        Ok(_) => { return Ok((State { n: s.n + 1 }, 1)); },
        Err(_) => { return Err("read"); }
    }
}
function (own s: State) step(reader: Reader): Result[(State, i32), string] {
    return s.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
function event(own s: State, reader: Reader): Result[(State, i32), string] {
    return s.step(reader);
}
`

func TestOwnGuardReturnsStateThroughReaderCallback(t *testing.T) {
	wantOK(t, "reader-callback-result", readerReturnDecls+`
function run(reader: Reader): i32 {
    let state = State { n: 0 };
    for i in 0..2 {
        let (next, value) = match (event(state, reader)) { Ok(v) => v, Err(_) => { return 1; } };
        state = next;
    }
    return state.n;
}`)
}

func TestOwnGuardStillRejectsReaderBorrowThroughMethod(t *testing.T) {
	wantE051(t, "reader-result-borrows", readerReturnDecls+`
function (s: State) lend(reader: Reader): Reader { return reader; }
function alias(s: State, reader: Reader): Reader { return s.lend(reader); }
function run(reader: Reader): i32 {
    let state = State { n: 0 };
    let copied = alias(state, reader);
    match (copied.close()) { Ok(_) => {}, Err(_) => {} }
    return 0;
}`)
}
