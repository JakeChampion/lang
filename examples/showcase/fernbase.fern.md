# Fernbase: transactions and an ordered primary index

This small SQL engine owns one table: `entries(key, value)`. Both columns contain text.
A sorted array supplies the primary index. Equality queries use binary search. Inserts preserve key order.
This design makes comparison counts visible without an external database engine.

A request executes its complete statement batch on a private value. Any error discards the batch.
The HTTP layer compares revisions before a commit. An optional journal synchronizes the commit before publication.

```fern
<<*>>=
<<SQL engine>>
```

## SQL engine

The lexer preserves quoted Unicode text and doubled single quotes. Keywords use ASCII case folding.
The parser accepts a documented SQL subset. Unsupported syntax returns a Result error.
Queries return at most 200 rows. Database size and transaction size have separate limits.

```fern
<<SQL engine>>=
import "std/array";
import "std/result";
import "std/option";
import "std/json";
import "std/string";
import "std/parse";
import "std/crypto";

@derive(json.Json, json.FromJson)
pub struct Row { key: string, value: string }

@derive(json.Json, json.FromJson)
pub struct Database { revision: i64, rows: Row[] }

@derive(json.Json)
pub struct Output { rows: Row[], plan: string, comparisons: i32, changed: boolean }

pub struct Execution { database: Database, output: Output }

pub function initial(): Database {
  return Database { revision: 0, rows: [] };
}

pub function lower_bound(rows: Row[], key: string): (i32, i32) {
  let lo = 0;
  let hi = rows.len();
  let comparisons = 0;
  while (lo < hi) {
    let mid = lo + (hi - lo) / 2;
    comparisons = comparisons + 1;
    if (rows[mid].key < key) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return (lo, comparisons);
}

pub function tokens(sql: string): Result[string[], string] {
  if (sql.len() > 12000) {
    return Err("SQL exceeds 12000 bytes.");
  }
  let out: string[] = [];
  let at = 0;
  while (at < sql.len()) {
    let byte = sql[at];
    if (byte == 32 || byte == 10 || byte == 13 || byte == 9) {
      at = at + 1;
      continue;
    }
    if (byte == 39) {
      at = at + 1;
      let text = "";
      let closed = false;
      while (at < sql.len()) {
        let start = at;
        while (at < sql.len() && sql[at] != 39) {
          at = at + 1;
        }
        text = text + sql.drop(start).take(at - start);
        if (at >= sql.len()) {
          break;
        }
        at = at + 1;
        if (at < sql.len() && sql[at] == 39) {
          text = text + "'";
          at = at + 1;
        } else {
          closed = true;
          break;
        }
      }
      if (!closed) {
        return Err("Close the quoted SQL string.");
      }
      out = out.append("'" + text);
      continue;
    }
    if (byte == 40 || byte == 41 || byte == 44 || byte == 61 || byte == 59 || byte == 42) {
      out = out.append(sql.drop(at).take(1));
      at = at + 1;
      continue;
    }
    let start = at;
    while (at < sql.len() && sql[at] != 32 && sql[at] != 10 && sql[at] != 13 && sql[at] != 9 && sql[at] != 40 && sql[at] != 41 && sql[at] != 44 && sql[at] != 61 && sql[at] != 59) {
      at = at + 1;
    }
    out = out.append(sql.drop(start).take(at - start).to_ascii_lower());
  }
  return Ok(out);
}

function literal(token: string): Result[string, string] {
  if (!token.starts_with("'")) {
    return Err("Use single quotes around text values.");
  }
  return Ok(token.drop(1));
}

function put(rows: Row[], row: Row, insert_only: boolean): Result[Row[], string] {
  if (row.key.len() == 0 || row.key.len() > 128 || row.value.len() > 4096) {
    return Err("A key needs 1 to 128 bytes. A value has at most 4096 bytes.");
  }
  let (at, comparisons) = lower_bound(rows, row.key);
  if (at < rows.len() && rows[at].key == row.key) {
    if (insert_only) {
      return Err("The primary key already exists.");
    }
    return Ok(rows.with(at, row));
  }
  if (!insert_only) {
    return Err("The primary key does not exist.");
  }
  if (rows.len() >= 10000) {
    return Err("This database already has 10000 rows.");
  }
  let result: Row[] = [];
  let i = 0;
  while (i < at) {
    result = result.append(rows[i]);
    i = i + 1;
  }
  result = result.append(row);
  while (i < rows.len()) {
    result = result.append(rows[i]);
    i = i + 1;
  }
  return Ok(result);
}

function statement(db: Database, t: string[]): Result[Execution, string] {
  let output = Output { rows: [] as Row[], plan: "", comparisons: 0, changed: false };
  if (t.len() == 0) {
    return Ok(Execution { database: db, output: output });
  }
  if (t[0] == "explain") {
    if (t.len() < 2 || t[1] != "select") {
      return Err("EXPLAIN supports SELECT statements.");
    }
    let result = statement(db, t.drop(1))?;
    return Ok(Execution { database: db, output: Output { ...result.output, rows: [] as Row[], changed: false } });
  }
  if (t.len() >= 4 && t[0] == "select" && t[1] == "*" && t[2] == "from" && t[3] == "entries") {
    if (t.len() == 4) {
      return Ok(Execution { database: db, output: Output { ...output, rows: db.rows.take(200), plan: "ORDERED SCAN entries PRIMARY KEY, first 200 rows", comparisons: db.rows.len().min(200) } });
    }
    if (t.len() != 8 || t[4] != "where" || t[5] != "key" || t[6] != "=") {
      return Err("Use SELECT * FROM entries WHERE key = 'text'.");
    }
    let key = literal(t[7])?;
    let (at, comparisons) = lower_bound(db.rows, key);
    let rows: Row[] = [];
    if (at < db.rows.len() && db.rows[at].key == key) {
      rows = rows.append(db.rows[at]);
    }
    return Ok(Execution { database: db, output: Output { ...output, rows: rows, comparisons: comparisons, plan: "BINARY SEARCH entries PRIMARY KEY" } });
  }
  if (t.len() == 9 && t[0] == "insert" && t[1] == "into" && t[2] == "entries" && t[3] == "values" && t[4] == "(" && t[6] == "," && t[8] == ")") {
    let rows = put(db.rows, Row { key: literal(t[5])?, value: literal(t[7])? }, true)?;
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: true, plan: "UNIQUE PRIMARY KEY INSERT" } });
  }
  if (t.len() == 10 && t[0] == "update" && t[1] == "entries" && t[2] == "set" && t[3] == "value" && t[4] == "=" && t[6] == "where" && t[7] == "key" && t[8] == "=") {
    let rows = put(db.rows, Row { key: literal(t[9])?, value: literal(t[5])? }, false)?;
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: true, plan: "PRIMARY KEY UPDATE" } });
  }
  if (t.len() == 7 && t[0] == "delete" && t[1] == "from" && t[2] == "entries" && t[3] == "where" && t[4] == "key" && t[5] == "=") {
    let key = literal(t[6])?;
    let rows: Row[] = [];
    for row in db.rows {
      if (row.key != key) {
        rows = rows.append(row);
      }
    }
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: rows.len() != db.rows.len(), plan: "PRIMARY KEY DELETE" } });
  }
  return Err("Use SELECT, EXPLAIN SELECT, INSERT, UPDATE, or DELETE on entries(key, value).");
}

pub function execute(database: Database, sql: string): Result[Execution, string] {
  let input = tokens(sql)?;
  let current: string[] = [];
  let db = database;
  let last = Output { rows: [] as Row[], plan: "No statements", comparisons: 0, changed: false };
  let changed = false;
  let count = 0;
  for token in input.append(";") {
    if (token == ";") {
      if (current.len() > 0) {
        count = count + 1;
        if (count > 128) {
          return Err("A transaction has at most 128 statements.");
        }
        let result = statement(db, current)?;
        db = result.database;
        last = result.output;
        changed = changed || last.changed;
        current = [];
      }
    } else {
      current = current.append(token);
    }
  }
  if (changed) {
    db = Database { ...db, revision: database.revision + 1 };
  }
  return Ok(Execution { database: db, output: Output { ...last, changed: changed } });
}
```
