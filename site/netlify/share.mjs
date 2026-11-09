// Short links for the playground. POST /api/share stores a playground hash
// (`#src=…`, with its other fields) and answers with its id. GET /p/ID
// redirects to the playground with that hash. The id is from a hash of the
// snippet, so the same snippet always gets the same link.

// The playground hash of a whole page of code fits in this many bytes.
export const MAX_HASH = 64 * 1024;

export async function shareId(hash) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(hash));
  return Buffer.from(digest).toString("base64url").slice(0, 10);
}

// `store` is a Netlify Blobs store, or anything with get and set. The id
// of a GET comes from the path, /p/ID.
export async function handle(req, store) {
  if (req.method === "POST") {
    let hash;
    try {
      ({ hash } = await req.json());
    } catch {
      return new Response("The body is not JSON.", { status: 400 });
    }
    if (typeof hash !== "string" || !hash.startsWith("#src=") || hash.length > MAX_HASH) {
      return new Response(`The hash must start with #src= and be ${MAX_HASH} bytes or less.`, { status: 400 });
    }
    const id = await shareId(hash);
    await store.set(id, hash);
    return Response.json({ id });
  }
  const id = /^\/p\/([A-Za-z0-9_-]+)$/.exec(new URL(req.url).pathname)?.[1];
  if (req.method === "GET" && id) {
    const hash = await store.get(id);
    if (hash === null || hash === undefined) {
      return new Response("No snippet has this link.", { status: 404 });
    }
    return new Response(null, { status: 302, headers: { location: `/playground/${hash}` } });
  }
  return new Response("Method not allowed.", { status: 405, headers: { allow: "GET, POST" } });
}
