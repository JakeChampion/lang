# The guestbook: the web without JavaScript

HTML forms, cookies, and redirects make a complete application. No browser script is required.
This example keeps the most recent 80 notes in process memory. A restart clears the notes.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Guestbook state>>
<<Sign form tokens>>
<<Render the wall>>
<<Validate and redirect>>
<<Publish an Atom feed>>
```

## Guestbook state

Each note stores text, time, a public identifier, and a private author hash.
A process-local random secret signs form tokens. It never appears in a response.

```fern
<<Guestbook state>>=
// Server-rendered forms, a signed double-submit token, and an Atom feed.
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "std/http/form";
import "std/http/cookie";
import "std/crypto";
import "std/time";
import "std/utf8";
import "./common";

pub struct Note { name: string, text: string, timestamp: i64, id: string, author_key: string }

pub struct State { entries: Cell[Note[]], secret: string, started_ms: i64 }

pub function state(): State {
  return State { entries: cell_new([] as Note[]), secret: common.token(), started_ms: now_unix_ms() };
}
```

## Sign form tokens

The signed double-submit pattern binds a hidden form token to a cookie.
HMAC verification rejects a token that an attacker creates. SameSite and HttpOnly add cookie restrictions.

```fern
<<Sign form tokens>>=
function signed(state: State, token: string): string {
  return token + "." + crypto.hmac_sha256_hex(state.secret.bytes(), token.bytes());
}

pub function valid_token(state: State, value: string): boolean {
  let parts: string[] = value.split(".");
  if (parts.len() != 2 || parts[0].len() != 24) {
    return false;
  }
  return crypto.hmac_verify_hex(state.secret.bytes(), parts[0].bytes(), parts[1]);
}
```

## Render the wall

Use a valid cookie token or create a new one. Include that token in a hidden form field.
Escape every user value before HTML output. The browser can submit the form with JavaScript disabled.

```fern
<<Render the wall>>=
pub function document(state: State, req: HttpRequest, notice: string, status: i32): HttpResponse {
  let token: string = req.cookie("fern-notes").unwrap_or("");
  if (!valid_token(state, token)) {
    token = signed(state, common.token());
  }
  let entries: Note[] = state.entries.get();
  let html: string = "<section class=\"notes-intro app-intro\"><div class=\"eyebrow\">05 / THE HYPERTEXT WEB</div><h1>Leave a little<br><em>something.</em></h1>"
    + "<p>A guestbook for this corner of the internet. No JavaScript. No account. Just a form, a server, and your words.</p>"
    + "<div class=\"tech-chips\"><span>Server-rendered HTML</span><span>POST → 303 → GET</span><span>Signed cookies</span><a href=\"/notes/feed.xml\">Atom feed ↗</a></div></section>";
  if (notice != "") {
    html = html + "<p class=\"notice\" role=\"status\">" + common.escape(notice) + "</p>";
  }
  html = html + "<div class=\"notes-layout\"><form class=\"note-form\" method=\"post\" action=\"/notes/\"><span class=\"eyebrow\">YOUR NOTE</span>"
    + "<input type=\"hidden\" name=\"token\" value=\"" + common.escape(token) + "\"><label for=\"name\">Name</label>"
    + "<input id=\"name\" name=\"name\" maxlength=\"48\" autocomplete=\"nickname\" required placeholder=\"What should we call you?\">"
    + "<label for=\"message\">A thought, a hello, a small discovery.</label><textarea id=\"message\" name=\"message\" maxlength=\"600\" rows=\"5\" required placeholder=\"I was here, and…\"></textarea>"
    + "<button class=\"button primary\" type=\"submit\">Pin your note ↗</button><p class=\"fine-print\">Notes are public. This demo keeps the last 80 notes in server memory and resets on restart.</p></form>"
    + "<section class=\"note-wall\" aria-label=\"Guestbook notes\">";
  if (entries.len() == 0) {
    html = html + "<article class=\"paper-note seed-note\"><span class=\"note-pin\"></span><p>The web can still feel like a place. Leave the first note.</p><footer>Fern, in action <span>✳</span></footer></article>";
  }
  let at: i32 = entries.len() - 1;
  while (at >= 0) {
    let note: Note = entries[at];
    html = html + "<article class=\"paper-note\" id=\"note-" + note.id + "\"><span class=\"note-pin\"></span><p>" + common.escape(note.text)
      + "</p><footer>" + common.escape(note.name) + "<a href=\"#note-" + note.id + "\" aria-label=\"Link to note\">↗</a></footer></article>";
    at = at - 1;
  }
  html = html + "</section></div><details class=\"how-it-works\"><summary>How Fern makes this work</summary><p>The server validates a signed cookie and form token, escapes the text, and returns a redirect. Refresh does not repeat the POST. This entire page works with JavaScript disabled.</p><a href=\"/_src/showcase/notes.fern\">Read the complete implementation ↗</a></details>";
  let response: HttpResponse = HttpResponse { ...http.html(common.page("The guestbook", "notes", html)), status: status };
  let item: cookie.SetCookie = cookie.SetCookie { ...cookie.new("fern-notes", token), path: "/notes", same_site: cookie.SameSite.Strict, secure: req.headers.get("X-Forwarded-Proto").unwrap_or("") == "https" };
  return response.with_cookie(item).unwrap_or(http.internal_error("cookie serialization failed")).with_header("Cache-Control", "no-store");
}
```

## Validate and redirect

Verify the token before a mutation. Count Unicode code points and reject forbidden control characters.
Limit each anonymous token to one note per three seconds. This is a demo limit, not an identity system.
A successful POST returns a 303 redirect. A page refresh then repeats only GET.

```fern
<<Validate and redirect>>=
pub function post(state: State, req: HttpRequest): HttpResponse {
  match (form.parse(req)) {
    Ok(fields) => {
      let token: string = fields.get("token").unwrap_or("");
      if (token != req.cookie("fern-notes").unwrap_or("") || !valid_token(state, token)) {
        return document(state, req, "This form expired. Please submit the new form below.", 403);
      }
      let name: string = fields.get("name").unwrap_or("").trim();
      let text: string = fields.get("message").unwrap_or("").trim();
      if (name.len() < 1 || utf8.codepoint_count(name) > 48 || text.len() < 1 || utf8.codepoint_count(text) > 600) {
        return document(state, req, "Please use a name of 48 characters and a note of 600 characters or fewer.", 422);
      }
      for c in (name + text).as_bytes() {
        if (c < 32 && c != 9 && c != 10 && c != 13) {
          return document(state, req, "Notes cannot contain control characters.", 422);
        }
      }
      let author_key: string = crypto.sha256_hex(token.bytes());
      let now: i64 = now_unix_ms();
      let entries: Note[] = state.entries.get();
      for recent in entries {
        if (recent.author_key == author_key && now - recent.timestamp < 3000) {
          return document(state, req, "Give your last note a moment to settle, then try again.", 429);
        }
      }
      let note: Note = Note { name: name, text: text, timestamp: now, id: common.token(), author_key: author_key };
      entries = entries.append(note);
      if (entries.len() > 80) {
        entries = entries.drop(1);
      }
      state.entries.set(entries);
      return common.redirect("/notes/?posted=1#note-" + note.id).with_header("Cache-Control", "no-store");
    },
    Err(_) => {
      return document(state, req, "Submit the guestbook form with a name and a note.", 415);
    }
  }
}
```

## Publish an Atom feed

A feed lets an old web protocol reach modern readers. Escape XML text with the same HTML escape rules.
Public note identifiers do not expose the form token. Feed dates use RFC 3339.

```fern
<<Publish an Atom feed>>=
pub function feed(state: State): HttpResponse {
  let entries: Note[] = state.entries.get();
  let updated_ms: i64 = state.started_ms;
  if (entries.len() > 0) {
    updated_ms = entries[entries.len() - 1].timestamp;
  }
  let updated: string = time.instant_from_unix(updated_ms / 1000).format_rfc3339();
  let xml: string = "<?xml version=\"1.0\" encoding=\"UTF-8\"?><feed xmlns=\"http://www.w3.org/2005/Atom\"><id>urn:fern:guestbook</id><title>The Fern guestbook</title><author><name>Fern community</name></author><updated>" + updated + "</updated>";
  for note in entries {
    xml = xml + "<entry><id>urn:fern:note:" + note.id + "</id><author><name>" + common.escape(note.name) + "</name></author><title>" + common.escape(note.name) + "</title><updated>" + time.instant_from_unix(note.timestamp / 1000).format_rfc3339() + "</updated><content type=\"text\">" + common.escape(note.text) + "</content></entry>";
  }
  return http.text(200, xml + "</feed>").with_content_type("application/atom+xml; charset=utf-8").with_header("Cache-Control", "no-cache");
}
```

## Check the signature

The example verifies a valid signature and rejects an altered token. The same secret never enters an HTTP response.

```fern test name=notes-signature
<<Guestbook state>>
<<Sign form tokens>>
function main(): i32 {
  let state = state();
  let token = signed(state, "0123456789abcdef01234567");
  if (!valid_token(state, token) || valid_token(state, token + "0")) { return 1; }
  return 0;
}
```
