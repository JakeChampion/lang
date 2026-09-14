# date(1) is one parse and one strftime per run, so the single-line
# rows are process startup plus the grammar; the -f row is the same
# work ten thousand times in one process, which is where the parser and
# the formatter show. The --debug row adds the trace, the zone rows the
# TZif read, and the many-conversion row every strftime directive at
# once.
d="$out/datebench"
mkdir -p "$d"
i=0
: > "$d/lines"
while [ "$i" -lt 10000 ]; do
  printf '2024-06-15 12:34:56.123456789 +0100 next monday 2 days ago\n' >> "$d/lines"
  i=$((i + 1))
done
printf 'date\ty\t{}\n'
printf 'date -d fixed\ty\tTZ=UTC0 {} -d "2024-06-15 12:34:56" +%%F\\ %%T\n'
printf 'date -d relative\ty\tTZ=America/New_York {} -d "TZ=\\"Asia/Tokyo\\" 12:34:56.5 next monday 3 months ago +0100"\n'
printf 'date every conversion\ty\tTZ=Europe/Berlin {} -d @1718434196 +%%a%%A%%b%%B%%c%%C%%d%%D%%e%%F%%g%%G%%h%%H%%I%%j%%k%%l%%m%%M%%n%%N%%p%%P%%q%%r%%R%%s%%S%%t%%T%%u%%U%%V%%w%%W%%x%%X%%y%%Y%%z%%:z%%::z%%:::z%%Z%%%%\n'
printf 'date -f 10000 lines\ty\tTZ=America/New_York {} -f %s/lines +%%F\\ %%T\\ %%N\\ %%z\n' "$d"
printf 'date -u -R\ty\t{} -u -R -d @1718434196\n'
printf 'date --debug\ty\tTZ=America/New_York {} --debug -d "2024-11-03 01:30 EDT 24 hours ago" 2>/dev/null\n'
