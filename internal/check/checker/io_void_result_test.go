package checker

import (
	"strings"
	"testing"
)

func TestHandleVoidOperationsComposeWithQuestionMark(t *testing.T) {
	src := `function f(r: Reader, d: Dir): Result[i32, IoError] {
		let w: Writer = stdout();
		w.write("hi\n")?;
		w.write_bytes([104 as u8])?;
		w.truncate(0 as i64)?;
		w.fsync()?;
		w.fdatasync()?;
		w.syncfs()?;
		w.drop_cache(0 as i64, 0 as i64)?;
		w.dup_onto(1)?;
		w.splice_forever("y\n")?;
		w.close()?;
		r.fsync()?;
		r.fdatasync()?;
		r.syncfs()?;
		r.drop_cache(0 as i64, 0 as i64)?;
		r.dup_onto(0)?;
		r.close()?;
		d.close()?;
		remove_file("x")?;
		return Ok(1);
	}`
	if got := checkSrc(t, src); got != "" {
		t.Fatalf("want no diagnostics, got:\n%s", got)
	}
}

func TestHandleWriteIsNotOption(t *testing.T) {
	src := `function f(): i32 {
		let w: Writer = stdout();
		match (w.write("x")) {
			Some(e) => { return 1; },
			None => { return 0; }
		}
	}`
	got := checkSrc(t, src)
	if !strings.Contains(got, `variant "Some" is not part of enum Result`) {
		t.Fatalf("want the Some arm refused against Result, got:\n%s", got)
	}
}
