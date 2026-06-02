package literate

import (
	"strings"
	"testing"
)

func TestUnusedChunksReachability(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // comma-joined sorted unused chunk names
	}{
		{
			"all used",
			"```fern\n<<*>>=\n<<a>>\n```\n```fern\n<<a>>=\nx\n```\n",
			"",
		},
		{
			"one orphan",
			"```fern\n<<*>>=\n<<a>>\n```\n```fern\n<<a>>=\nx\n```\n```fern\n<<orphan>>=\ny\n```\n",
			"orphan",
		},
		{
			"dead subtree: orphan refs inner, both unused",
			"```fern\n<<*>>=\nmain\n```\n```fern\n<<orphan>>=\n<<inner>>\n```\n```fern\n<<inner>>=\nz\n```\n",
			"inner,orphan",
		},
		{
			"chunk used only by a file-root is reached",
			"```fern file=m.fern\n<<lib>>\n```\n```fern\n<<lib>>=\npub fn f() {}\n```\n",
			"",
		},
		{
			"root never reported even with no refs",
			"```fern\n<<*>>=\nfn main() {}\n```\n",
			"",
		},
	}
	for _, c := range cases {
		got := strings.Join(Parse(c.src).UnusedChunks(), ",")
		if got != c.want {
			t.Errorf("%s: UnusedChunks = %q, want %q", c.name, got, c.want)
		}
	}
}
