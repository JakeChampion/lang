# Lantern: an inverted document index

The archive stores Unicode text and an inverted index. Each term maps to document IDs and occurrence counts.
A query visits the matching posting lists. BM25 scores account for term rarity and document length.
A bounded top-20 selection uses document ID as the tie breaker.

```fern
<<*>>=
<<Search engine>>
```

## Search engine

Unicode lowercase conversion precedes punctuation and whitespace tokenization. CJK text without spaces remains one term.
The index accepts 4096 documents. Each document contains at most 8192 bytes of text.
Queries contain at most 16 distinct terms. Results report the actual number of posting entries visited.

```fern
<<Search engine>>=
import "std/array";
import "std/string";
import "std/result";
import "std/option";
import "std/json";
import "std/f64";
import "core/map";

@derive(json.Json, json.FromJson)
pub struct Document { id: i32, title: string, text: string }

pub struct Posting { id: i32, frequency: i32 }

pub struct Index { documents: Document[], lengths: i32[], terms: Map[string, Posting[]], words: i32, bytes: i32 }

@derive(json.Json)
pub struct Hit { id: i32, title: string, text: string, score: f64, matched: i32 }

@derive(json.Json)
pub struct Results { hits: Hit[], documents: i32, terms: i32, postings_read: i32, query_terms: string[] }

pub function empty(): Index {
  return Index { documents: [], lengths: [], terms: map_new(128), words: 0, bytes: 0 };
}

pub function tokenize(text: string): string[] {
  let clean = text.to_lower();
  for separator in ["\n", "\r", "\t", ".", ",", ";", ":", "!", "?", "(", ")", "[", "]", "{", "}", "\"", "'", "/", "\\", "-", "—", "　"] {
    clean = clean.replace(separator, " ");
  }
  let terms: string[] = [];
  for term in clean.split(" ") {
    if (term.len() > 0 && term.len() <= 128) {
      terms = terms.append(term);
    }
  }
  return terms;
}

pub function add(index: Index, document: Document): Result[Index, string] {
  if (index.documents.len() >= 4096 || document.id != index.documents.len() || document.title.len() == 0 || document.title.len() > 256 || document.text.len() > 8192) {
    return Err("Each document needs a title and at most 8192 bytes of text. The archive holds 4096 documents.");
  }
  if (index.bytes + document.title.len() + document.text.len() > 4194304) {
    return Err("The archive exceeds its 4 MiB text limit.");
  }
  let words = tokenize(document.title + " " + document.text);
  if (index.words + words.len() > 262144) {
    return Err("The archive exceeds its 262144-token limit.");
  }
  let counts: Map[string, i32] = map_new(128);
  for term in words {
    counts = counts.insert(term, counts.get(term).unwrap_or(0) + 1);
  }
  let terms = index.terms;
  for entry in counts.entries() {
    terms = terms.insert(entry.0, terms.get(entry.0).unwrap_or([] as Posting[]).append(Posting { id: document.id, frequency: entry.1 }));
  }
  if (terms.len() > 65536) {
    return Err("The archive exceeds its 65536-term vocabulary limit.");
  }
  return Ok(Index { documents: index.documents.append(document), lengths: index.lengths.append(words.len()), terms: terms, words: index.words + words.len(), bytes: index.bytes + document.title.len() + document.text.len() });
}

pub function search(index: Index, query: string): Results {
  let output: Hit[] = [];
  let seen: Map[string, boolean] = map_new(16);
  let terms: string[] = [];
  for term in tokenize(query.take(1024)) {
    if (!seen.contains(term) && terms.len() < 16) {
      seen = seen.insert(term, true);
      terms = terms.append(term);
    }
  }
  let scores: Map[i32, f64] = map_new(64);
  let matched: Map[i32, i32] = map_new(64);
  let visits = 0;
  let average = (index.words as f64 / (index.documents.len().max(1) as f64)).max(1.0);
  for term in terms {
    let postings = index.terms.get(term).unwrap_or([] as Posting[]);
    let idf = (1.0 + (index.documents.len() as f64 - postings.len() as f64 + 0.5) / (postings.len() as f64 + 0.5)).log();
    for posting in postings {
      visits = visits + 1;
      let frequency = posting.frequency as f64;
      let score = idf * frequency * 2.2 / (frequency + 1.2 * (0.25 + 0.75 * (index.lengths[posting.id] as f64) / average));
      scores = scores.insert(posting.id, scores.get(posting.id).unwrap_or(0.0) + score);
      matched = matched.insert(posting.id, matched.get(posting.id).unwrap_or(0) + 1);
    }
  }
  for entry in scores.entries() {
    let doc = index.documents[entry.0];
    let hit = Hit { id: doc.id, title: doc.title, text: doc.text.take(280), score: entry.1, matched: matched.get(doc.id).unwrap_or(0) };
    let next: Hit[] = [];
    let inserted = false;
    for old in output {
      if (!inserted && (hit.score > old.score || hit.score == old.score && hit.id < old.id)) {
        next = next.append(hit);
        inserted = true;
      }
      if (next.len() < 20) {
        next = next.append(old);
      }
    }
    if (!inserted && next.len() < 20) {
      next = next.append(hit);
    }
    output = next.take(20);
  }
  return Results { hits: output, documents: index.documents.len(), terms: index.terms.len(), postings_read: visits, query_terms: terms };
}
```
