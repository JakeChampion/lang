package x86tbl

type Cond struct {
	Code      byte
	Spellings []string
}

var Conds = []Cond{
	{0, []string{"o"}},
	{1, []string{"no"}},
	{2, []string{"b", "c", "nae"}},
	{3, []string{"ae", "nb", "nc"}},
	{4, []string{"e", "z"}},
	{5, []string{"ne", "nz"}},
	{6, []string{"be", "na"}},
	{7, []string{"a", "nbe"}},
	{8, []string{"s"}},
	{9, []string{"ns"}},
	{10, []string{"p"}},
	{11, []string{"np"}},
	{12, []string{"l", "nge"}},
	{13, []string{"ge", "nl"}},
	{14, []string{"le", "ng"}},
	{15, []string{"g", "nle"}},
}

func CondSpellings() []string {
	out := make([]string, 0, 28)
	for _, c := range Conds {
		out = append(out, c.Spellings...)
	}
	return out
}
