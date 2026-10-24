package e2ecompiler

import (
	"bytes"
	"testing"
)

func countUserCalls(asm []byte, callee string) int {
	needle := []byte("call " + callee)
	helperPrefix := []byte("__fn___fern_")
	count := 0
	inHelper := false
	for _, line := range bytes.Split(asm, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if isFunctionLabel(line, trimmed) {
			inHelper = bytes.HasPrefix(trimmed, helperPrefix)
			continue
		}
		if !inHelper && bytes.Contains(line, needle) {
			count++
		}
	}
	return count
}

func isFunctionLabel(line, trimmed []byte) bool {
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '.' {
		return false
	}
	return bytes.HasSuffix(trimmed, []byte(":")) && !bytes.HasSuffix(trimmed, []byte(".r:")) && !bytes.HasSuffix(trimmed, []byte(".k:"))
}

func countCallsInFn(t testing.TB, asm []byte, fn string, callee string) int {
	t.Helper()
	needle := []byte("call " + callee)
	label := []byte("__fn_" + fn + ":")
	count := 0
	inFn, found := false, false
	for _, line := range bytes.Split(asm, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if isFunctionLabel(line, trimmed) {
			inFn = bytes.Equal(trimmed, label)
			found = found || inFn
			continue
		}
		if inFn && bytes.Contains(line, needle) {
			count++
		}
	}
	if !found {
		t.Fatalf("the asm defines no __fn_%s to count calls in", fn)
	}
	return count
}

func countUserStrFreeReclaims(asm []byte) int {
	return countUserCalls(asm, "__fn___fern_str_free")
}

func countUserArrBoxAllocs(asm []byte) int {
	return countUserCalls(asm, "__fern_arr_box")
}
