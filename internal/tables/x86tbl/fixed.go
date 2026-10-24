package x86tbl

type FixedOp struct {
	Spellings  []string
	Bytes      []byte
	Repeatable bool
}

var FixedOps = []FixedOp{
	{Spellings: []string{"ret"}, Bytes: []byte{0xC3}, Repeatable: true},
	{Spellings: []string{"syscall"}, Bytes: []byte{0x0F, 0x05}},
	{Spellings: []string{"leave"}, Bytes: []byte{0xC9}},
	{Spellings: []string{"cld"}, Bytes: []byte{0xFC}},
	{Spellings: []string{"std"}, Bytes: []byte{0xFD}},
	{Spellings: []string{"nop"}, Bytes: []byte{0x90}, Repeatable: true},
	{Spellings: []string{"int3"}, Bytes: []byte{0xCC}},
	{Spellings: []string{"pause"}, Bytes: []byte{0xF3, 0x90}},
	{Spellings: []string{"mfence"}, Bytes: []byte{0x0F, 0xAE, 0xF0}},
	{Spellings: []string{"lfence"}, Bytes: []byte{0x0F, 0xAE, 0xE8}},
	{Spellings: []string{"sfence"}, Bytes: []byte{0x0F, 0xAE, 0xF8}},
	{Spellings: []string{"cbtw", "cbw"}, Bytes: []byte{0x66, 0x98}},
	{Spellings: []string{"cwtl", "cwde"}, Bytes: []byte{0x98}},
	{Spellings: []string{"cltq", "cdqe"}, Bytes: []byte{0x48, 0x98}},
	{Spellings: []string{"cwtd", "cwd"}, Bytes: []byte{0x66, 0x99}},
	{Spellings: []string{"cltd", "cdq"}, Bytes: []byte{0x99}},
	{Spellings: []string{"cqto", "cqo"}, Bytes: []byte{0x48, 0x99}},
	{Spellings: []string{"pushfq", "pushf"}, Bytes: []byte{0x9C}},
	{Spellings: []string{"popfq", "popf"}, Bytes: []byte{0x9D}},
	{Spellings: []string{"ud2"}, Bytes: []byte{0x0F, 0x0B}},

	{Spellings: []string{"movsb"}, Bytes: []byte{0xA4}, Repeatable: true},
	{Spellings: []string{"movsw"}, Bytes: []byte{0x66, 0xA5}, Repeatable: true},
	{Spellings: []string{"movsd", "movsl"}, Bytes: []byte{0xA5}, Repeatable: true},
	{Spellings: []string{"movsq"}, Bytes: []byte{0x48, 0xA5}, Repeatable: true},
	{Spellings: []string{"stosb"}, Bytes: []byte{0xAA}, Repeatable: true},
	{Spellings: []string{"stosw"}, Bytes: []byte{0x66, 0xAB}, Repeatable: true},
	{Spellings: []string{"stosl"}, Bytes: []byte{0xAB}, Repeatable: true},
	{Spellings: []string{"stosq"}, Bytes: []byte{0x48, 0xAB}, Repeatable: true},
	{Spellings: []string{"lodsb"}, Bytes: []byte{0xAC}, Repeatable: true},
	{Spellings: []string{"lodsw"}, Bytes: []byte{0x66, 0xAD}, Repeatable: true},
	{Spellings: []string{"lodsl"}, Bytes: []byte{0xAD}, Repeatable: true},
	{Spellings: []string{"lodsq"}, Bytes: []byte{0x48, 0xAD}, Repeatable: true},
	{Spellings: []string{"scasb"}, Bytes: []byte{0xAE}, Repeatable: true},
	{Spellings: []string{"scasw"}, Bytes: []byte{0x66, 0xAF}, Repeatable: true},
	{Spellings: []string{"scasl"}, Bytes: []byte{0xAF}, Repeatable: true},
	{Spellings: []string{"scasq"}, Bytes: []byte{0x48, 0xAF}, Repeatable: true},
	{Spellings: []string{"cmpsb"}, Bytes: []byte{0xA6}, Repeatable: true},
	{Spellings: []string{"cmpsw"}, Bytes: []byte{0x66, 0xA7}, Repeatable: true},
	{Spellings: []string{"cmpsd", "cmpsl"}, Bytes: []byte{0xA7}, Repeatable: true},
	{Spellings: []string{"cmpsq"}, Bytes: []byte{0x48, 0xA7}, Repeatable: true},
}
