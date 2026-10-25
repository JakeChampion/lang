# Dates, times, and durations

This module provides durations, monotonic instants, wall-clock timestamps, and calendar dates.

## Model

A `Duration` is a signed span of time. It holds whole seconds and a nanosecond part in the range 0 to 999,999,999.
An `Instant` is a reading of a monotonic clock, in the same two parts. Its origin is not specified, so only the difference between two instants has a meaning. Use instants for deadlines and for elapsed time.
A `Timestamp` is a wall-clock time in seconds and nanoseconds since 1970-01-01T00:00:00Z. Use timestamps for dates and for values that other systems read.
Calendar dates use the proleptic Gregorian calendar.
This module never reads a clock, so it reaches no host capability. The clock module reads the wall clock as a `Timestamp` and the monotonic clock as an `Instant`.
The time-zone module holds the time-zone type and converts timestamps to local civil time.

## Contracts

Duration, instant, and timestamp arithmetic saturates. A result outside the range of `Duration` becomes `DURATION_MAX` or `DURATION_MIN`.
The `as_*` conversions truncate toward zero. A count outside the `i64` range saturates.
Division of a duration by zero gives `DURATION_ZERO`, as integer division by zero gives zero.

## Executable example

```fern test name=std-time-example
import "core/cmp";
import "std/time";

function main(): i32 {
  let budget: Duration = time.duration_seconds(2).add(time.duration_millis(500));
  if (budget.as_millis() != 2500 || budget.mul(2).as_seconds() != 5) { return 1; }
  let start: Instant = Instant { sec: 100, nsec: 0 };
  let deadline: Instant = start.add(budget);
  if (deadline.since(start).cmp(budget) != 0) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const NANOS_PER_SECOND: i32>>

<<4 pub struct Timestamp>>

<<durations>>

<<monotonic instants>>

<<5 pub function days_from_civil(y: i64, m: i32, d: i32): i64>>

<<6 pub function is_leap_year(y: i32): boolean>>

<<7 function __pad2(n: i32): string>>

<<8 pub function (d: Date) format_iso(): string>>

<<9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean>>

<<10 pub function timestamp_parse_rfc3339(s: string): Option[Timestamp]>>

<<11 function __weekday_short(n: i32): string>>

<<12 pub function month_name(m: i32): string>>

<<13 function __month_from_short(s: string, start: i32): i32>>

<<14 pub function http_date_parse(s: string): Option[Timestamp]>>

<<15 function __http_timestamp(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Timestamp]>>

<<17 pub function timestamp_parse_rfc3339_offset(s: string): Option[(Timestamp, i32)]>>

<<18 pub function span_years(n: i32): Span>>

<<19 function __rel_unit(n: i64, unit: string): string>>

<<20 pub function (t: Timestamp) relative_to(now: Timestamp): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides durations, monotonic instants, timestamps, and calendar dates.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/i64";
import "std/string";
```

### `3 pub const NANOS_PER_SECOND: i32`

```fern
<<3 pub const NANOS_PER_SECOND: i32>>=
pub const NANOS_PER_SECOND: i32 = 1000000000;

pub const NANOS_PER_MILLI: i32 = 1000000;

pub const NANOS_PER_MICRO: i32 = 1000;

pub const SECONDS_PER_MINUTE: i32 = 60;

pub const SECONDS_PER_HOUR: i32 = 3600;

pub const SECONDS_PER_DAY: i32 = 86400;

pub const MINUTES_PER_HOUR: i32 = 60;

pub const HOURS_PER_DAY: i32 = 24;

pub const DAYS_PER_WEEK: i32 = 7;
```

### `4 pub struct Timestamp`

```fern
<<4 pub struct Timestamp>>=
pub struct Timestamp { sec: i64, nsec: i32 }

pub function timestamp_from_unix(sec: i64): Timestamp {
  return Timestamp { sec: sec, nsec: 0 };
}

// Saturates like `Duration.add`.
pub function (t: Timestamp) add(d: Duration): Timestamp {
  let r: Duration = Duration { sec: t.sec, nsec: t.nsec }.add(d);
  return Timestamp { sec: r.sec, nsec: r.nsec };
}

pub function (t: Timestamp) sub(d: Duration): Timestamp {
  let r: Duration = Duration { sec: t.sec, nsec: t.nsec }.sub(d);
  return Timestamp { sec: r.sec, nsec: r.nsec };
}

pub function timestamp_from_unix_millis(ms: i64): Timestamp {
  let d: Duration = duration_millis(ms);
  return Timestamp { sec: d.sec, nsec: d.nsec };
}

// Milliseconds since the epoch; saturates at the i64 range.
pub function (t: Timestamp) unix_millis(): i64 {
  return Duration { sec: t.sec, nsec: t.nsec }.as_millis();
}

// Negative when `earlier` is later than `t`.
pub function (t: Timestamp) since(earlier: Timestamp): Duration {
  return Duration { sec: t.sec, nsec: t.nsec }.sub(Duration { sec: earlier.sec, nsec: earlier.nsec });
}

pub function (a: Timestamp) eq(b: Timestamp): boolean {
  return a.sec == b.sec && a.nsec == b.nsec;
}

pub function (a: Timestamp) cmp(b: Timestamp): i32 {
  return __cmp_parts(a.sec, a.nsec, b.sec, b.nsec);
}

pub function date_make(year: i32, month: i32, day: i32): Date {
  return Date { year: year, month: month, day: day };
}

pub function time_make(hour: i32, minute: i32, second: i32): Time {
  return Time { hour: hour, minute: minute, second: second, nsec: 0 };
}

pub function datetime_make(d: Date, t: Time): DateTime {
  return DateTime { date: d, time: t };
}

pub function span_days(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: n,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function span_hours(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: n, minutes: 0, seconds: 0, nanos: 0,
  };
}
```

### `durations`

Every constructor normalizes the nanosecond part into 0 to 999,999,999.
A negative duration therefore has a negative `sec` and a non-negative `nsec`.

```fern
<<durations>>=
pub const DURATION_ZERO: Duration = Duration { sec: 0, nsec: 0 };

pub const DURATION_MAX: Duration = Duration { sec: 9223372036854775807, nsec: 999999999 };

pub const DURATION_MIN: Duration = Duration { sec: 0 - 9223372036854775807 - 1, nsec: 0 };

pub function duration_hours(n: i64): Duration {
  return __duration_whole(n, 3600);
}

pub function duration_minutes(n: i64): Duration {
  return __duration_whole(n, 60);
}

pub function duration_seconds(n: i64): Duration {
  return Duration { sec: n, nsec: 0 };
}

pub function duration_millis(n: i64): Duration {
  return __duration_part(n, 1000, 1000000);
}

pub function duration_micros(n: i64): Duration {
  return __duration_part(n, 1000000, 1000);
}

pub function duration_nanos(n: i64): Duration {
  return __duration_part(n, 1000000000, 1);
}

function __duration_whole(n: i64, secs_per_unit: i64): Duration {
  if (n > 9223372036854775807 / secs_per_unit) {
    return DURATION_MAX;
  }
  if (n < (0 - 9223372036854775807 - 1) / secs_per_unit) {
    return DURATION_MIN;
  }
  return Duration { sec: n * secs_per_unit, nsec: 0 };
}

function __duration_part(n: i64, units_per_sec: i64, nanos_per_unit: i32): Duration {
  return Duration { sec: n.div_euclid(units_per_sec), nsec: n.rem_euclid(units_per_sec) as i32 * nanos_per_unit };
}

pub function (d: Duration) as_hours(): i64 {
  return d.as_seconds() / 3600;
}

pub function (d: Duration) as_minutes(): i64 {
  return d.as_seconds() / 60;
}

pub function (d: Duration) as_seconds(): i64 {
  return __duration_count(d, 1, 1000000000);
}

pub function (d: Duration) as_millis(): i64 {
  return __duration_count(d, 1000, 1000000);
}

pub function (d: Duration) as_micros(): i64 {
  return __duration_count(d, 1000000, 1000);
}

pub function (d: Duration) as_nanos(): i64 {
  return __duration_count(d, 1000000000, 1);
}

pub function (d: Duration) as_seconds_f64(): f64 {
  return d.sec as f64 + d.nsec as f64 / 1000000000.0;
}

// Whole units, truncated toward zero.
function __duration_count(d: Duration, units_per_sec: i64, nanos_per_unit: i32): i64 {
  if (d.sec < 0 && d.nsec > 0) {
    return (d.sec + 1) *| units_per_sec -| ((1000000000 - d.nsec) / nanos_per_unit) as i64;
  }
  return d.sec *| units_per_sec +| (d.nsec / nanos_per_unit) as i64;
}

pub function (a: Duration) eq(b: Duration): boolean {
  return a.sec == b.sec && a.nsec == b.nsec;
}

pub function (a: Duration) cmp(b: Duration): i32 {
  return __cmp_parts(a.sec, a.nsec, b.sec, b.nsec);
}

function __cmp_parts(a_sec: i64, a_nsec: i32, b_sec: i64, b_nsec: i32): i32 {
  if (a_sec != b_sec) {
    if (a_sec < b_sec) {
      return 0 - 1;
    }
    return 1;
  }
  if (a_nsec < b_nsec) {
    return 0 - 1;
  }
  if (a_nsec > b_nsec) {
    return 1;
  }
  return 0;
}

pub function (d: Duration) is_zero(): boolean {
  return d.sec == 0 && d.nsec == 0;
}

pub function (d: Duration) is_negative(): boolean {
  return d.sec < 0;
}

pub function (a: Duration) add(b: Duration): Duration {
  let Some(sec) = a.sec.checked_add(b.sec) else {
    return __duration_bound(b.sec);
  };
  return __duration_carry(sec, a.nsec + b.nsec);
}

pub function (a: Duration) sub(b: Duration): Duration {
  let Some(sec) = a.sec.checked_sub(b.sec) else {
    return __duration_bound(0 - 1 - b.sec);
  };
  return __duration_carry(sec, a.nsec - b.nsec);
}

pub function (d: Duration) mul(n: i32): Duration {
  let nanos: i64 = d.nsec as i64 * (n as i64);
  let Some(sec) = d.sec.checked_mul(n as i64) else {
    return __duration_bound(d.sec.signum() * (n as i64));
  };
  let Some(total) = sec.checked_add(nanos.div_euclid(1000000000)) else {
    return __duration_bound(nanos);
  };
  return Duration { sec: total, nsec: nanos.rem_euclid(1000000000) as i32 };
}

// Truncates toward zero.
pub function (d: Duration) div(n: i32): Duration {
  if (n == 0) {
    return DURATION_ZERO;
  }
  let m: Duration = d;
  if (d.sec < 0) {
    m = __duration_neg(d);
  }
  let k: i64 = n as i64;
  if (k < 0) {
    k = 0 - k;
  }
  let rem: i64 = m.sec % k;
  let q: Duration = Duration { sec: m.sec / k, nsec: ((rem * 1000000000 + m.nsec as i64) / k) as i32 };
  if (d.sec < 0 != n < 0) {
    return __duration_neg(q);
  }
  return q;
}

function __duration_neg(d: Duration): Duration {
  if (d.nsec == 0) {
    if (d.sec == 0 - 9223372036854775807 - 1) {
      return DURATION_MAX;
    }
    return Duration { sec: 0 - d.sec, nsec: 0 };
  }
  return Duration { sec: 0 - 1 - d.sec, nsec: 1000000000 - d.nsec };
}

// `nsec` lies strictly between -1e9 and 2e9.
function __duration_carry(sec: i64, nsec: i32): Duration {
  if (nsec >= 1000000000) {
    let Some(up) = sec.checked_add(1) else {
      return DURATION_MAX;
    };
    return Duration { sec: up, nsec: nsec - 1000000000 };
  }
  if (nsec < 0) {
    let Some(down) = sec.checked_sub(1) else {
      return DURATION_MIN;
    };
    return Duration { sec: down, nsec: nsec + 1000000000 };
  }
  return Duration { sec: sec, nsec: nsec };
}

function __duration_bound(sign: i64): Duration {
  if (sign < 0) {
    return DURATION_MIN;
  }
  return DURATION_MAX;
}

pub function (d: Duration) to_string(): string {
  let total_ms: i64 = d.as_millis();
  if (total_ms == 0) {
    return "0s";
  }
  let neg: boolean = total_ms < 0;
  let ms: i64 = total_ms;
  if (neg) {
    ms = 0 - ms;
  }
  let h: i64 = ms / 3600000;
  let m: i64 = ms % 3600000 / 60000;
  let s: i64 = ms % 60000 / 1000;
  let msec: i64 = ms % 1000;
  let out: string = "";
  if (neg) {
    out = "-";
  }
  if (h > 0) {
    out = out + h.to_string() + "h";
  }
  if (m > 0) {
    out = out + m.to_string() + "m";
  }
  if (s > 0) {
    out = out + s.to_string() + "s";
  }
  if (msec > 0) {
    out = out + msec.to_string() + "ms";
  }
  return out;
}
```

### `monotonic instants`

```fern
<<monotonic instants>>=
pub function (i: Instant) add(d: Duration): Instant {
  let r: Duration = Duration { sec: i.sec, nsec: i.nsec }.add(d);
  return Instant { sec: r.sec, nsec: r.nsec };
}

pub function (i: Instant) sub(d: Duration): Instant {
  let r: Duration = Duration { sec: i.sec, nsec: i.nsec }.sub(d);
  return Instant { sec: r.sec, nsec: r.nsec };
}

// Negative when `earlier` is later than `i`.
pub function (i: Instant) since(earlier: Instant): Duration {
  return Duration { sec: i.sec, nsec: i.nsec }.sub(Duration { sec: earlier.sec, nsec: earlier.nsec });
}

pub function (a: Instant) eq(b: Instant): boolean {
  return a.sec == b.sec && a.nsec == b.nsec;
}

pub function (a: Instant) cmp(b: Instant): i32 {
  return __cmp_parts(a.sec, a.nsec, b.sec, b.nsec);
}

// The clock reading in nanoseconds, for the library's own integer bookkeeping.
pub(package) function instant_nanos(i: Instant): i64 {
  return Duration { sec: i.sec, nsec: i.nsec }.as_nanos();
}

pub(package) function instant_from_nanos(ns: i64): Instant {
  let d: Duration = duration_nanos(ns);
  return Instant { sec: d.sec, nsec: d.nsec };
}

// The millisecond timeout the runtime's wait primitives take: -1 waits without a limit.
// A partial millisecond rounds up, so a short positive timeout still waits.
pub(package) function timeout_millis(t: Option[Duration]): i32 {
  let Some(d) = t else {
    return 0 - 1;
  };
  if (d.sec < 0 || d.is_zero()) {
    return 0;
  }
  let ms: i64 = d.add(duration_nanos(999999)).as_millis();
  if (ms > 2147483647) {
    return 2147483647;
  }
  return ms as i32;
}

pub(package) function millis_timeout(ms: i32): Option[Duration] {
  if (ms < 0) {
    return None;
  }
  return Some(duration_millis(ms as i64));
}
```

### `5 pub function days_from_civil(y: i64, m: i32, d: i32): i64`

These functions convert between civil dates and days since 1970-01-01.
They use the proleptic Gregorian calendar and accept any `i64` year. A civil date is the tuple of year, month, and day.

```fern
<<5 pub function days_from_civil(y: i64, m: i32, d: i32): i64>>=
pub function days_from_civil(y: i64, m: i32, d: i32): i64 {
  let yy: i64 = y;
  if (m <= 2) {
    yy = yy - 1;
  }
  let era: i64 = yy.div_euclid(400);
  let yoe: i64 = yy - era * 400;
  let mp: i32 = m + 9;
  if (m > 2) {
    mp = m - 3;
  }
  let doy: i64 = ((153 * mp + 2) / 5) as i64 + d as i64 - 1;
  let doe: i64 = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  return era * 146097 + doe - 719468;
}

pub function civil_from_days(days: i64): (i64, i32, i32) {
  let z: i64 = days + 719468;
  let era: i64 = z.div_euclid(146097);
  let doe: i64 = z - era * 146097;
  let yoe: i64 = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
  let y: i64 = yoe + era * 400;
  let doy: i64 = doe - (365 * yoe + yoe / 4 - yoe / 100);
  let mp: i64 = (5 * doy + 2) / 153;
  let d: i32 = (doy - (153 * mp + 2) / 5 + 1) as i32;
  let m: i32 = (mp + 3) as i32;
  if (mp >= 10) {
    m = (mp - 9) as i32;
  }
  if (m <= 2) {
    y = y + 1;
  }
  return (y, m, d);
}

function __date_from_days(days: i64): Date {
  let c: (i64, i32, i32) = civil_from_days(days);
  return Date { year: c.0 as i32, month: c.1, day: c.2 };
}

// The UTC civil date and clock time of `t`.
pub function (t: Timestamp) to_datetime(): DateTime {
  let sid: i32 = t.sec.rem_euclid(86400) as i32;
  return DateTime {
    date: __date_from_days(t.sec.div_euclid(86400)),
    time: Time { hour: sid / 3600, minute: sid / 60 % 60, second: sid % 60, nsec: t.nsec },
  };
}
```

### `6 pub function is_leap_year(y: i32): boolean`

```fern
<<6 pub function is_leap_year(y: i32): boolean>>=
pub function is_leap_year(y: i32): boolean {
  if (y % 4 != 0) {
    return false;
  }
  if (y % 100 != 0) {
    return true;
  }
  return y % 400 == 0;
}

pub function days_in_month(y: i32, m: i32): i32 {
  if (m == 1) {
    return 31;
  }
  if (m == 2) {
    if (is_leap_year(y)) {
      return 29;
    }
    return 28;
  }
  if (m == 3) {
    return 31;
  }
  if (m == 4) {
    return 30;
  }
  if (m == 5) {
    return 31;
  }
  if (m == 6) {
    return 30;
  }
  if (m == 7) {
    return 31;
  }
  if (m == 8) {
    return 31;
  }
  if (m == 9) {
    return 30;
  }
  if (m == 10) {
    return 31;
  }
  if (m == 11) {
    return 30;
  }
  if (m == 12) {
    return 31;
  }
  return 0;
}

pub function (d: Date) is_valid(): boolean {
  if (d.month < 1) {
    return false;
  }
  if (d.month > 12) {
    return false;
  }
  if (d.day < 1) {
    return false;
  }
  return d.day <= days_in_month(d.year, d.month);
}

pub function (d: Date) add_days(n: i32): Date {
  return __date_from_days(days_from_civil(d.year as i64, d.month, d.day) + n as i64);
}

pub function (d: Date) days_since(other: Date): i32 {
  return (days_from_civil(d.year as i64, d.month, d.day) - days_from_civil(other.year as i64, other.month, other.day)) as i32;
}

// 0 is Sunday.
pub function (d: Date) weekday(): i32 {
  return (days_from_civil(d.year as i64, d.month, d.day) + 4).rem_euclid(7) as i32;
}

pub function (d: Date) day_of_year(): i32 {
  let m: i32 = d.month;
  let mp: i32 = 0;
  if (m > 2) {
    mp = m - 3;
  } else {
    mp = m + 9;
  }
  let doy: i32 = (153 * mp + 2) / 5 + d.day;
  if (m <= 2) {
    return doy - 306;
  }
  let add_for_leap: i32 = 0;
  if (is_leap_year(d.year)) {
    add_for_leap = 1;
  }
  return doy + 59 + add_for_leap;
}
```

### `7 function __pad2(n: i32): string`

```fern
<<7 function __pad2(n: i32): string>>=
function __pad2(n: i32): string {
  if (n < 10) {
    return "0" + int.int_to_string(n);
  }
  return int.int_to_string(n);
}

function __pad4(n: i32): string {
  if (n >= 0 && n < 10) {
    return "000" + n.to_string();
  }
  if (n >= 0 && n < 100) {
    return "00" + n.to_string();
  }
  if (n >= 0 && n < 1000) {
    return "0" + n.to_string();
  }
  return n.to_string();
}

function __parse_digits(s: string, start: i32, end: i32): i32 {
  if (start >= end) {
    return -1;
  }
  let acc: i32 = 0;
  let i: i32 = start;
  while (i < end) {
    let b: i32 = s[i] as i32;
    if (b < 48 || b > 57) {
      return -1;
    }
    acc = acc * 10 + (b - 48);
    i = i + 1;
  }
  return acc;
}
```

### `8 pub function (d: Date) format_iso(): string`

```fern
<<8 pub function (d: Date) format_iso(): string>>=
pub function (d: Date) format_iso(): string {
  return __pad4(d.year) + "-" + __pad2(d.month) + "-" + __pad2(d.day);
}

pub function date_parse_iso(s: string): Option[Date] {
  if (s.len() != 10) {
    return None;
  }
  if (s[4] != 45 || s[7] != 45) {
    return None;
  }
  let y: i32 = __parse_digits(s, 0, 4);
  let m: i32 = __parse_digits(s, 5, 7);
  let d: i32 = __parse_digits(s, 8, 10);
  if (y < 0 || m < 0 || d < 0) {
    return None;
  }
  return Some(Date { year: y, month: m, day: d });
}

pub function (t: Timestamp) format_rfc3339(): string {
  let dt: DateTime = t.to_datetime();
  let head: string = dt.date.format_iso() + "T" + __pad2(dt.time.hour) + ":" + __pad2(dt.time.minute) + ":" + __pad2(dt.time.second);
  if (t.nsec == 0) {
    return head + "Z";
  }
  return head + "." + __fraction(t.nsec) + "Z";
}

// The nine fractional-second digits of `ns`.
function __fraction(ns: i32): string {
  let div: i32 = 100000000;
  let frac: string = "";
  while (div >= 1) {
    let digit: i32 = ns / div - ns / (div * 10) * 10;
    if (digit == 0) {
      frac = frac + "0";
    } else if (digit == 1) {
      frac = frac + "1";
    } else if (digit == 2) {
      frac = frac + "2";
    } else if (digit == 3) {
      frac = frac + "3";
    } else if (digit == 4) {
      frac = frac + "4";
    } else if (digit == 5) {
      frac = frac + "5";
    } else if (digit == 6) {
      frac = frac + "6";
    } else if (digit == 7) {
      frac = frac + "7";
    } else if (digit == 8) {
      frac = frac + "8";
    } else {
      frac = frac + "9";
    }
    if (div == 1) {
      div = 0;
    } else {
      div = div / 10;
    }
  }
  return frac;
}
```

### `9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean`

```fern
<<9 function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean>>=
function __rfc3339_fields_valid(y: i32, mo: i32, d: i32, h: i32, mn: i32, sc: i32): boolean {
  if (!Date { year: y, month: mo, day: d }.is_valid()) {
    return false;
  }
  if (h > 23 || mn > 59 || sc > 59) {
    return false;
  }
  return true;
}
```

### `10 pub function timestamp_parse_rfc3339(s: string): Option[Timestamp]`

```fern
<<10 pub function timestamp_parse_rfc3339(s: string): Option[Timestamp]>>=
pub function timestamp_parse_rfc3339(s: string): Option[Timestamp] {
  let Some(parsed) = timestamp_parse_rfc3339_offset(s) else {
    return None;
  };
  return Some(parsed.0);
}
```

### `11 function __weekday_short(n: i32): string`

```fern
<<11 function __weekday_short(n: i32): string>>=
function __weekday_short(n: i32): string {
  if (n == 0) {
    return "Sun";
  }
  if (n == 1) {
    return "Mon";
  }
  if (n == 2) {
    return "Tue";
  }
  if (n == 3) {
    return "Wed";
  }
  if (n == 4) {
    return "Thu";
  }
  if (n == 5) {
    return "Fri";
  }
  if (n == 6) {
    return "Sat";
  }
  return "";
}

function __month_short(m: i32): string {
  if (m == 1) {
    return "Jan";
  }
  if (m == 2) {
    return "Feb";
  }
  if (m == 3) {
    return "Mar";
  }
  if (m == 4) {
    return "Apr";
  }
  if (m == 5) {
    return "May";
  }
  if (m == 6) {
    return "Jun";
  }
  if (m == 7) {
    return "Jul";
  }
  if (m == 8) {
    return "Aug";
  }
  if (m == 9) {
    return "Sep";
  }
  if (m == 10) {
    return "Oct";
  }
  if (m == 11) {
    return "Nov";
  }
  if (m == 12) {
    return "Dec";
  }
  return "";
}
```

### `12 pub function month_name(m: i32): string`

```fern
<<12 pub function month_name(m: i32): string>>=
pub function month_name(m: i32): string {
  if (m == 1) {
    return "January";
  }
  if (m == 2) {
    return "February";
  }
  if (m == 3) {
    return "March";
  }
  if (m == 4) {
    return "April";
  }
  if (m == 5) {
    return "May";
  }
  if (m == 6) {
    return "June";
  }
  if (m == 7) {
    return "July";
  }
  if (m == 8) {
    return "August";
  }
  if (m == 9) {
    return "September";
  }
  if (m == 10) {
    return "October";
  }
  if (m == 11) {
    return "November";
  }
  if (m == 12) {
    return "December";
  }
  return "";
}

pub function (d: Date) weekday_name(): string {
  let n: i32 = d.weekday();
  if (n == 0) {
    return "Sunday";
  }
  if (n == 1) {
    return "Monday";
  }
  if (n == 2) {
    return "Tuesday";
  }
  if (n == 3) {
    return "Wednesday";
  }
  if (n == 4) {
    return "Thursday";
  }
  if (n == 5) {
    return "Friday";
  }
  return "Saturday";
}

pub function (t: Timestamp) format_http_date(): string {
  let dt: DateTime = t.to_datetime();
  let d: Date = dt.date;
  return __weekday_short(d.weekday()) + ", " + __pad2(d.day) + " "
    + __month_short(d.month) + " " + __pad4(d.year) + " "
    + __pad2(dt.time.hour) + ":" + __pad2(dt.time.minute) + ":" + __pad2(dt.time.second) + " GMT";
}
```

### `13 function __month_from_short(s: string, start: i32): i32`

```fern
<<13 function __month_from_short(s: string, start: i32): i32>>=
function __month_from_short(s: string, start: i32): i32 {
  if (start + 3 > s.len()) {
    return -1;
  }
  let m: str = s.slice_snap(start, start + 3);
  if (m == "Jan") {
    return 1;
  }
  if (m == "Feb") {
    return 2;
  }
  if (m == "Mar") {
    return 3;
  }
  if (m == "Apr") {
    return 4;
  }
  if (m == "May") {
    return 5;
  }
  if (m == "Jun") {
    return 6;
  }
  if (m == "Jul") {
    return 7;
  }
  if (m == "Aug") {
    return 8;
  }
  if (m == "Sep") {
    return 9;
  }
  if (m == "Oct") {
    return 10;
  }
  if (m == "Nov") {
    return 11;
  }
  if (m == "Dec") {
    return 12;
  }
  return -1;
}
```

### `14 pub function http_date_parse(s: string): Option[Timestamp]`

```fern
<<14 pub function http_date_parse(s: string): Option[Timestamp]>>=
pub function http_date_parse(s: string): Option[Timestamp] {
  if (s.len() == 29) {
    return __imf_fixdate(s);
  }
  if (s.len() == 24) {
    return __asctime_date(s);
  }
  return None;
}

pub function http_date_parse_at(s: string, now: Timestamp): Option[Timestamp] {
  match (http_date_parse(s)) {
    Some(i) => {
      return Some(i);
    },
    None => {}
  }
  return __rfc850_date(s, now);
}
```

### `15 function __http_timestamp(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Timestamp]`

```fern
<<15 function __http_timestamp(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Timestamp]>>=
function __http_timestamp(year: i32, mo: i32, day: i32, h: i32, mn: i32, sc: i32): Option[Timestamp] {
  if (year < 0 || mo < 1 || day < 1 || day > days_in_month(year, mo) || !__time_of_day_ok(h, mn, sc)) {
    return None;
  }
  let sec: i64 = days_from_civil(year as i64, mo, day) * 86400 + (h * 3600 + mn * 60 + sc) as i64;
  return Some(Timestamp { sec: sec, nsec: 0 });
}

function __asctime_date(s: string): Option[Timestamp] {
  if (s[3] != 32 || s[7] != 32 || s[10] != 32 || s[19] != 32 || s[13] != 58 || s[16] != 58) {
    return None;
  }
  let day_from: i32 = 8;
  if (s[8] == 32) {
    day_from = 9;
  }
  return __http_timestamp(__parse_digits(s, 20, 24), __month_from_short(s, 4), __parse_digits(s, day_from, 10), __parse_digits(s, 11, 13), __parse_digits(s, 14, 16), __parse_digits(s, 17, 19));
}

function __rfc850_date(s: string, now: Timestamp): Option[Timestamp] {
  let Some(c) = s.index_of(",") else {
    return None;
  };
  if (c < 6 || s.len() != c + 24 || !__rfc850_punctuation(s, c)) {
    return None;
  }
  let yy: i32 = __parse_digits(s, c + 9, c + 11);
  if (yy < 0) {
    return None;
  }
  return __http_timestamp(__rfc850_year(yy, now), __month_from_short(s, c + 5), __parse_digits(s, c + 2, c + 4), __parse_digits(s, c + 12, c + 14), __parse_digits(s, c + 15, c + 17), __parse_digits(s, c + 18, c + 20));
}

function __rfc850_punctuation(s: string, c: i32): boolean {
  if (s[c + 1] != 32 || s[c + 4] != 45 || s[c + 8] != 45 || s[c + 11] != 32) {
    return false;
  }
  return s[c + 14] == 58 && s[c + 17] == 58 && s[c + 20] == 32 && s.slice_snap(c + 21, c + 24) == "GMT";
}

function __rfc850_year(yy: i32, now: Timestamp): i32 {
  let now_year: i32 = now.to_datetime().date.year;
  let y: i32 = now_year - now_year % 100 + yy;
  if (y > now_year + 50) {
    return y - 100;
  }
  if (y + 100 <= now_year + 50) {
    return y + 100;
  }
  return y;
}

function __time_of_day_ok(h: i32, mn: i32, sc: i32): boolean {
  return h >= 0 && h <= 23 && mn >= 0 && mn <= 59 && sc >= 0 && sc <= 60;
}

function __imf_fixdate(s: string): Option[Timestamp] {
  if (!__imf_punctuation(s)) {
    return None;
  }
  return __http_timestamp(__parse_digits(s, 12, 16), __month_from_short(s, 8), __parse_digits(s, 5, 7), __parse_digits(s, 17, 19), __parse_digits(s, 20, 22), __parse_digits(s, 23, 25));
}

function __imf_punctuation(s: string): boolean {
  if (s[3] != 44 || s[4] != 32 || s[7] != 32 || s[11] != 32 || s[16] != 32 || s[25] != 32) {
    return false;
  }
  return s[19] == 58 && s[22] == 58 && s.slice_snap(26, 29) == "GMT";
}
```

### `17 pub function timestamp_parse_rfc3339_offset(s: string): Option[(Timestamp, i32)]`

```fern
<<17 pub function timestamp_parse_rfc3339_offset(s: string): Option[(Timestamp, i32)]>>=
// The timestamp and the UTC offset in seconds of an RFC 3339 time with a `Z` or `±hh:mm` offset.
pub function timestamp_parse_rfc3339_offset(s: string): Option[(Timestamp, i32)] {
  let n: i32 = s.len();
  if (n < 20) {
    return None;
  }
  if (s[4] != 45 || s[7] != 45) {
    return None;
  }
  if (s[10] != 84) {
    return None;
  }
  if (s[13] != 58 || s[16] != 58) {
    return None;
  }
  let y: i32 = __parse_digits(s, 0, 4);
  let mo: i32 = __parse_digits(s, 5, 7);
  let d: i32 = __parse_digits(s, 8, 10);
  let h: i32 = __parse_digits(s, 11, 13);
  let mn: i32 = __parse_digits(s, 14, 16);
  let sc: i32 = __parse_digits(s, 17, 19);
  if (y < 0 || mo < 0 || d < 0 || h < 0 || mn < 0 || sc < 0) {
    return None;
  }
  if (!__rfc3339_fields_valid(y, mo, d, h, mn, sc)) {
    return None;
  }
  let pos: i32 = 19;
  let nsec: i32 = 0;
  if (s[pos] == 46) {
    let frac_start: i32 = pos + 1;
    let frac_end: i32 = -1;
    let i: i32 = frac_start;
    while (i < n) {
      let b: i32 = s[i] as i32;
      if (b == 90 || b == 43 || b == 45) {
        frac_end = i;
        i = n;
      } else {
        i = i + 1;
      }
    }
    if (frac_end < 0) {
      return None;
    }
    if (frac_end == frac_start) {
      return None;
    }
    if (frac_end - frac_start > 9) {
      return None;
    }
    let frac: i32 = __parse_digits(s, frac_start, frac_end);
    if (frac < 0) {
      return None;
    }
    let pad: i32 = 9 - (frac_end - frac_start);
    while (pad > 0) {
      frac = frac * 10;
      pad = pad - 1;
    }
    nsec = frac;
    pos = frac_end;
  }
  let offset_seconds: i32 = 0;
  if (s[pos] == 90) {
    if (pos + 1 != n) {
      return None;
    }
  } else if (s[pos] == 43 || s[pos] == 45) {
    if (pos + 6 != n) {
      return None;
    }
    if (s[pos + 3] != 58) {
      return None;
    }
    let oh: i32 = __parse_digits(s, pos + 1, pos + 3);
    let om: i32 = __parse_digits(s, pos + 4, pos + 6);
    if (oh < 0 || om < 0) {
      return None;
    }
    if (oh > 23 || om > 59) {
      return None;
    }
    offset_seconds = oh * 3600 + om * 60;
    if (s[pos] == 45) {
      offset_seconds = -offset_seconds;
    }
  } else {
    return None;
  }
  let local_sec: i64 = days_from_civil(y as i64, mo, d) * 86400 + (h * 3600 + mn * 60 + sc) as i64;
  return Some((Timestamp { sec: local_sec - offset_seconds as i64, nsec: nsec }, offset_seconds));
}

pub function span_seconds(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: n, nanos: 0,
  };
}

pub function span_minutes(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: n, seconds: 0, nanos: 0,
  };
}

pub function span_weeks(n: i32): Span {
  return Span {
    years: 0, months: 0, weeks: n, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function span_months(n: i32): Span {
  return Span {
    years: 0, months: n, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}
```

### `18 pub function span_years(n: i32): Span`

```fern
<<18 pub function span_years(n: i32): Span>>=
pub function span_years(n: i32): Span {
  return Span {
    years: n, months: 0, weeks: 0, days: 0,
    hours: 0, minutes: 0, seconds: 0, nanos: 0,
  };
}

pub function (d: Date) add_span(s: Span): Date {
  let serial_month: i64 = d.year as i64 * 12 + (d.month - 1 + s.years * 12 + s.months) as i64;
  let new_year: i32 = serial_month.div_euclid(12) as i32;
  let new_month: i32 = serial_month.rem_euclid(12) as i32 + 1;
  let max_day: i32 = days_in_month(new_year, new_month);
  let clamped_day: i32 = d.day;
  if (clamped_day > max_day) {
    clamped_day = max_day;
  }
  let anchor: Date = Date { year: new_year, month: new_month, day: clamped_day };
  let total_days: i32 = s.weeks * 7 + s.days;
  if (total_days == 0) {
    return anchor;
  }
  return anchor.add_days(total_days);
}

pub function (d: Date) days_until(other: Date): Span {
  return span_days(other.days_since(d));
}
```

### `19 function __rel_unit(n: i64, unit: string): string`

```fern
<<19 function __rel_unit(n: i64, unit: string): string>>=
function __rel_unit(n: i64, unit: string): string {
  let s: string = n.to_string() + " " + unit;
  if (n != 1 as i64) {
    s = s + "s";
  }
  return s;
}

function __rel_phrase(secs: i64): string {
  if (secs < 60 as i64) {
    return __rel_unit(secs, "second");
  }
  let mins: i64 = secs / (60 as i64);
  if (mins < 60 as i64) {
    return __rel_unit(mins, "minute");
  }
  let hours: i64 = mins / (60 as i64);
  if (hours < 24 as i64) {
    return __rel_unit(hours, "hour");
  }
  let days: i64 = hours / (24 as i64);
  if (days < 30 as i64) {
    return __rel_unit(days, "day");
  }
  let months: i64 = days / (30 as i64);
  if (months < 12 as i64) {
    return __rel_unit(months, "month");
  }
  let years: i64 = days / (365 as i64);
  return __rel_unit(years, "year");
}
```

### `20 pub function (t: Timestamp) relative_to(now: Timestamp): string`

```fern
<<20 pub function (t: Timestamp) relative_to(now: Timestamp): string>>=
pub function (t: Timestamp) relative_to(now: Timestamp): string {
  let raw: i64 = t.sec - now.sec;
  let mag: i64 = raw;
  if (mag < 0 as i64) {
    mag = 0 as i64 - mag;
  }
  if (mag < 5 as i64) {
    return "just now";
  }
  let phrase: string = __rel_phrase(mag);
  if (raw < 0 as i64) {
    return phrase + " ago";
  }
  return "in " + phrase;
}
```
