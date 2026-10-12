package fernsmith

// MainBytesDeletions is every corpus ShrinkMainBytes may try in one deletion
// step: data less one recorded list element.
func MainBytesDeletions(data []byte) [][]byte {
	_, spans := mainBytesSpans(data)
	out := make([][]byte, 0, len(spans))
	for _, s := range spans {
		if data[s.count] > 0 {
			out = append(out, withoutSpan(data, s))
		}
	}
	return out
}
