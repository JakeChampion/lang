// Package tzdata builds the IANA time zone table that std/tz embeds, so
// `tz.iana_zone(name)` answers on every target, wasm included, without
// reading /usr/share/zoneinfo at run time.
//
// # Source
//
// The data is the zoneinfo.zip that ships with the pinned Go toolchain
// (mise.toml), which Go's lib/time/update.bash builds from one IANA
// release with zic. The release and the archive's SHA-256 are pinned
// below; a toolchain bump that moves tzdata fails Load until both are
// updated and the table regenerated, so a tzdata bump is a
// regenerate-and-diff. Every name in the archive is included, aliases
// too: identical records are stored once, so an alias costs only its
// name.
//
// # Subset
//
// Transitions before 1970-01-01T00:00:00Z are dropped. The state in
// force at the epoch becomes the zone's opening entry, so an instant
// before 1970 reads that offset rather than its historical one. From
// the last transition on, the zone's POSIX TZ footer rule governs, which
// is what answers dates past the table, however far out.
//
// # Encoding
//
// Two Fern string literals. The names literal is `;Name=offset` per zone,
// sorted, with a trailing `;`, where `offset` is the decimal index into
// the records literal. A record is
//
//	footer|abbr:off:dst,abbr:off:dst,...|transitions;
//
// The first type is the opening entry. `transitions` is one entry per
// transition: the time since the previous one (the first since the
// epoch) as a varint, then one alphabet character naming its type. A
// delta that is a whole number of minutes is stored as minutes*2, any
// other as seconds*2+1. A varint is little-endian groups of five bits,
// one alphabet character each, with 32 added to every group but the
// last. No character in either literal needs an escape.
package tzdata

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
)

// Version is the IANA release the pinned toolchain's zoneinfo.zip holds.
const Version = "2026c"

// ArchiveSHA256 pins that archive's bytes.
const ArchiveSHA256 = "b2d18a7c8fa8142097a48c99609fb3c92db5ee98bc740294e57eab8ae9f94779"

// Alphabet is the 64 digits of the transition encoding.
const Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz+-"

// Zone is one TZif file's content from the epoch on.
type Zone struct {
	Types  []Type  // Types[0] is in force from the epoch to Times[0]
	Times  []int64 // ascending, all >= 0
	Idx    []int   // Idx[i] is the type in force from Times[i]
	Footer string  // POSIX TZ rule past the last transition; may be empty
}

// Type is one local time type.
type Type struct {
	Off  int32
	DST  bool
	Abbr string
}

// ArchivePath is where the pinned toolchain keeps zoneinfo.zip.
func ArchivePath() (string, error) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "lib", "time", "zoneinfo.zip"), nil
}

// Load reads every TZif file from the pinned archive, keyed by zone name.
func Load() (map[string][]byte, error) {
	path, err := ArchivePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != ArchiveSHA256 {
		return nil, fmt.Errorf("%s has SHA-256 %s, want %s (tzdata %s): the Go toolchain is not the one mise.toml pins, "+
			"or a toolchain bump moved tzdata; pin the new release and archive hash in internal/tables/tzdata and regenerate",
			path, got, ArchiveSHA256, Version)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[f.Name] = b
	}
	return files, nil
}

// header is a TZif header's six counts.
type header struct{ isut, isstd, leap, time, typ, char int }

func readHeader(b []byte) (header, error) {
	if len(b) < 44 || string(b[:4]) != "TZif" {
		return header{}, errors.New("not TZif")
	}
	n := func(i int) int { return int(binary.BigEndian.Uint32(b[20+4*i:])) }
	return header{n(0), n(1), n(2), n(3), n(4), n(5)}, nil
}

func (h header) size(width int) int {
	return h.time*width + h.time + h.typ*6 + h.char + h.leap*(width+4) + h.isstd + h.isut
}

// Parse reads the 64-bit block of a version 2+ TZif file and cuts it at
// the epoch.
func Parse(b []byte) (Zone, error) {
	h1, err := readHeader(b)
	if err != nil {
		return Zone{}, err
	}
	if b[4] < '2' {
		return Zone{}, errors.New("TZif version 1 has no 64-bit block")
	}
	b = b[44+h1.size(4):]
	h, err := readHeader(b)
	if err != nil {
		return Zone{}, err
	}
	if len(b) < 44+h.size(8) {
		return Zone{}, errors.New("truncated TZif")
	}
	body := b[44:]
	times := make([]int64, h.time)
	for i := range times {
		times[i] = int64(binary.BigEndian.Uint64(body[8*i:]))
	}
	idx := body[8*h.time : 9*h.time]
	tb := body[9*h.time:]
	chars := tb[6*h.typ : 6*h.typ+h.char]
	types := make([]Type, h.typ)
	for i := range types {
		e := tb[6*i:]
		a := chars[e[5]:]
		if j := bytes.IndexByte(a, 0); j >= 0 {
			a = a[:j]
		}
		types[i] = Type{Off: int32(binary.BigEndian.Uint32(e)), DST: e[4] != 0, Abbr: string(a)}
	}
	footer := string(body[h.size(8):])
	footer = strings.TrimSuffix(strings.TrimPrefix(footer, "\n"), "\n")
	return cut(types, times, idx, footer), nil
}

// firstType is the type before the first transition, chosen the way Go's
// time package chooses it (lookupFirstZone).
func firstType(types []Type, idx []byte) int {
	used := false
	for _, k := range idx {
		if k == 0 {
			used = true
		}
	}
	if !used {
		return 0
	}
	if len(idx) > 0 && types[idx[0]].DST {
		for k := int(idx[0]) - 1; k >= 0; k-- {
			if !types[k].DST {
				return k
			}
		}
	}
	for k := range types {
		if !types[k].DST {
			return k
		}
	}
	return 0
}

// cut keeps the transitions at or after the epoch and the types they
// use, renumbered in order of first use with the opening type first.
func cut(types []Type, times []int64, idx []byte, footer string) Zone {
	open := firstType(types, idx)
	start := 0
	for start < len(times) && times[start] < 0 {
		open = int(idx[start])
		start++
	}
	z := Zone{Footer: footer}
	remap := map[int]int{}
	use := func(k int) int {
		if n, ok := remap[k]; ok {
			return n
		}
		remap[k] = len(z.Types)
		z.Types = append(z.Types, types[k])
		return remap[k]
	}
	use(open)
	for i := start; i < len(times); i++ {
		z.Times = append(z.Times, times[i])
		z.Idx = append(z.Idx, use(int(idx[i])))
	}
	return z
}

func digit(d int) byte { return Alphabet[d] }

// Undigit is digit's inverse, -1 outside the alphabet.
func Undigit(c byte) int { return strings.IndexByte(Alphabet, c) }

func putVarint(b *strings.Builder, v int64) {
	for v >= 32 {
		b.WriteByte(digit(int(v&31) + 32))
		v >>= 5
	}
	b.WriteByte(digit(int(v)))
}

// Encode renders one record, terminator included.
func (z Zone) Encode() (string, error) {
	var b strings.Builder
	if strings.ContainsAny(z.Footer, "|;\"\\") {
		return "", fmt.Errorf("footer %q holds a separator", z.Footer)
	}
	if len(z.Types) > len(Alphabet) {
		return "", fmt.Errorf("%d types, more than one digit names", len(z.Types))
	}
	b.WriteString(z.Footer)
	b.WriteByte('|')
	for i, t := range z.Types {
		if t.Abbr == "" || strings.ContainsAny(t.Abbr, ":,|;\"\\") {
			return "", fmt.Errorf("abbreviation %q is empty or holds a separator", t.Abbr)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		dst := "0"
		if t.DST {
			dst = "1"
		}
		fmt.Fprintf(&b, "%s:%d:%s", t.Abbr, t.Off, dst)
	}
	b.WriteByte('|')
	prev := int64(0)
	for i, t := range z.Times {
		d := t - prev
		prev = t
		if d%60 == 0 {
			putVarint(&b, d/60*2)
		} else {
			putVarint(&b, d*2+1)
		}
		b.WriteByte(digit(z.Idx[i]))
	}
	b.WriteByte(';')
	return b.String(), nil
}

// Decode reads the record at the start of s back, the inverse of Encode.
func Decode(s string) (Zone, error) {
	end := strings.IndexByte(s, ';')
	if end < 0 {
		return Zone{}, errors.New("unterminated record")
	}
	parts := strings.SplitN(s[:end], "|", 3)
	if len(parts) != 3 {
		return Zone{}, errors.New("record has no three fields")
	}
	z := Zone{Footer: parts[0]}
	for _, e := range strings.Split(parts[1], ",") {
		f := strings.Split(e, ":")
		if len(f) != 3 {
			return Zone{}, fmt.Errorf("bad type %q", e)
		}
		off, err := strconv.Atoi(f[1])
		if err != nil {
			return Zone{}, err
		}
		z.Types = append(z.Types, Type{Off: int32(off), DST: f[2] == "1", Abbr: f[0]})
	}
	tr := parts[2]
	t := int64(0)
	for i := 0; i < len(tr); {
		v, shift := int64(0), uint(0)
		for {
			d := Undigit(tr[i])
			i++
			v |= int64(d&31) << shift
			shift += 5
			if d < 32 {
				break
			}
		}
		if v%2 == 0 {
			t += v / 2 * 60
		} else {
			t += v / 2
		}
		z.Times = append(z.Times, t)
		z.Idx = append(z.Idx, Undigit(tr[i]))
		i++
	}
	return z, nil
}

// Table is the two encoded literals.
type Table struct {
	Names   string
	Records string
	Zones   int // names
	Unique  int // distinct records
}

// Build encodes every zone in files.
func Build(files map[string][]byte) (Table, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var recs, idx strings.Builder
	at := map[string]int{}
	idx.WriteByte(';')
	for _, n := range names {
		if strings.ContainsAny(n, ";=\"\\") {
			return Table{}, fmt.Errorf("zone name %q holds a separator", n)
		}
		z, err := Parse(files[n])
		if err != nil {
			return Table{}, fmt.Errorf("%s: %w", n, err)
		}
		rec, err := z.Encode()
		if err != nil {
			return Table{}, fmt.Errorf("%s: %w", n, err)
		}
		off, ok := at[rec]
		if !ok {
			off = recs.Len()
			at[rec] = off
			recs.WriteString(rec)
		}
		fmt.Fprintf(&idx, "%s=%d;", n, off)
	}
	return Table{Names: idx.String(), Records: recs.String(), Zones: len(names), Unique: len(at)}, nil
}

// Begin and End bracket the generated block in std/tz.
const (
	Begin = "// ---- BEGIN GENERATED tzdata (go run ./cmd/tzdatagen) ----"
	End   = "// ---- END GENERATED tzdata ----"
)

// Block is the Fern source between Begin and End.
func (t Table) Block() string {
	return fmt.Sprintf(`// IANA tzdata %s, every zone and alias of the Go toolchain's
// zoneinfo.zip, from 1970 on: %d names, %d distinct records. The
// encoding is documented in internal/tables/tzdata. Do not edit by hand.
pub function iana_version(): string {
  return %q;
}

function iana_names(): string {
  return "%s";
}

function iana_records(): string {
  return "%s";
}
`, Version, t.Zones, t.Unique, Version, t.Names, t.Records)
}

// Rewrite replaces the generated block in src.
func (t Table) Rewrite(src string) (string, error) {
	i := strings.Index(src, Begin)
	j := strings.Index(src, End)
	if i < 0 || j < i {
		return "", errors.New("generated tzdata markers not found")
	}
	return fmtsource.Format(src[:i] + Begin + "\n" + t.Block() + src[j:])
}

// TzFern is std/tz's path from the repository root.
const TzFern = "internal/stdlib/std/tz.fern"

// Generate is std/tz's source with the table regenerated from the
// pinned archive.
func Generate(src string) (string, error) {
	files, err := Load()
	if err != nil {
		return "", err
	}
	t, err := Build(files)
	if err != nil {
		return "", err
	}
	return t.Rewrite(src)
}
