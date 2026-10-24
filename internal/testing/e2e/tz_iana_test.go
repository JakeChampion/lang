package e2e

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/tables/tzdata"
)

var ianaSpread = []string{
	"America/New_York", "America/Los_Angeles", "America/St_Johns", "America/Sao_Paulo",
	"America/Santiago", "America/Nuuk", "America/Havana", "Europe/London", "Europe/Dublin",
	"Europe/Moscow", "Europe/Lisbon", "Africa/Casablanca", "Africa/Cairo", "Asia/Jerusalem",
	"Asia/Tehran", "Asia/Kolkata", "Asia/Kathmandu", "Asia/Tokyo", "Australia/Sydney",
	"Australia/Lord_Howe", "Pacific/Auckland", "Pacific/Chatham", "Pacific/Apia",
	"Pacific/Kiritimati", "Antarctica/Troll", "Etc/GMT+5", "UTC", "US/Pacific",
}

func ianaQuery(loc *time.Location, sec int64) string {
	at := time.Unix(sec, 0).In(loc)
	abbr, off := at.Zone()
	d := 0
	if at.IsDST() {
		d = 1
	}
	return fmt.Sprintf("%d=%d/%s/%d", sec, off, abbr, d)
}

func ianaInstants(loc *time.Location, from, to int) []int64 {
	var out []int64
	end := time.Date(to, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(from, 1, 1, 0, 0, 0, 0, time.UTC).In(loc)
	for {
		_, next := at.ZoneBounds()
		if next.IsZero() || !next.Before(end) {
			break
		}
		if !next.After(at) {
			at = at.Add(time.Second)
			continue
		}
		out = append(out, next.Unix()-1, next.Unix())
		at = next
	}
	for y := from; y < to; y++ {
		jan := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
		out = append(out, jan-3*3600, jan, jan+3*3600, time.Date(y, 7, 1, 0, 0, 0, 0, time.UTC).Unix())
	}
	kept := out[:0]
	for _, sec := range out {
		if sec >= 0 {
			kept = append(kept, sec)
		}
	}
	return kept
}

func ianaFarFuture(files map[string][]byte) []string {
	seen := map[string]bool{}
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		z, err := tzdata.Parse(files[n])
		if err != nil || seen[z.Footer] {
			continue
		}
		seen[z.Footer] = true
		out = append(out, n)
	}
	return out
}

func ianaData(t *testing.T) (string, int) {
	files, err := tzdata.Load()
	if err != nil {
		t.Fatal(err)
	}
	var zones []string
	n := 0
	add := func(name string, spans ...[2]int) {
		loc, err := time.LoadLocationFromTZData(name, files[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var qs []string
		for _, s := range spans {
			for _, sec := range ianaInstants(loc, s[0], s[1]) {
				qs = append(qs, ianaQuery(loc, sec))
			}
		}
		n += len(qs)
		zones = append(zones, name+":"+strings.Join(qs, ","))
	}
	for _, name := range ianaSpread {
		add(name, [2]int{1970, 2041})
	}
	for _, name := range ianaFarFuture(files) {
		add(name, [2]int{2100, 2101}, [2]int{2400, 2401})
	}
	return strings.Join(zones, ";"), n
}

const ianaProgram = `import "std/tz";
import "std/string";

function data(): string {
  return "%s";
}

function digits(s: string): i64 {
  let v: i64 = 0;
  let i: i32 = 0;
  while (i < s.len()) {
    v = v * (10 as i64) + (s[i] as i32 - 48) as i64;
    i = i + 1;
  }
  return v;
}

function render(z: tz.Zone, sec: i64): string {
  let e: tz.Entry = z.entry_at(sec);
  let d: string = "0";
  if (e.dst) {
    d = "1";
  }
  return sec.to_string() + "=" + e.off.to_string() + "/" + e.abbrev + "/" + d;
}

function main(): i32 {
  let zones: string[] = data().split(";");
  let n: i32 = 0;
  let bad: i32 = 0;
  let i: i32 = 0;
  while (i < zones.len()) {
    let parts: string[] = zones[i].split(":");
    match (tz.iana_zone(parts[0])) {
      Some(z) => {
        let qs: string[] = parts[1].split(",");
        let j: i32 = 0;
        while (j < qs.len()) {
          let got: string = render(z, digits(qs[j].split("=")[0]));
          if (got != qs[j]) {
            bad = bad + 1;
            if (bad <= 20) {
              print(parts[0] + " got " + got + " want " + qs[j]);
            }
          }
          n = n + 1;
          j = j + 1;
        }
      },
      None => {
        print(parts[0] + " missing");
        bad = bad + 1;
      }
    }
    i = i + 1;
  }
  print("checked " + n.to_string() + " mismatched " + bad.to_string());
  return 0;
}
`

func TestIanaZonesMatchGoTime(t *testing.T) {
	data, n := ianaData(t)
	src := fmt.Sprintf(ianaProgram, data)
	want := fmt.Sprintf("checked %d mismatched 0", n)
	backendsAgree(t, src, interpOracle(t, src, want))
}
