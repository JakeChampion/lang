// Every public declaration of the standard library, for the search on
// /stdlib/functions/.
import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { pageEntries } from "../lib/stdlib-index.mjs";

export const GET: APIRoute = async () => {
  const pages = (await getCollection("docs")).filter((e) => e.id.startsWith("stdlib/"));
  const entries = pages
    .flatMap((e) => pageEntries(e.id.slice("stdlib/".length), e.data.title, e.body ?? ""))
    .map((x) => [x.name, x.module, x.page, x.id, x.signature]);
  return new Response(JSON.stringify(entries), { headers: { "content-type": "application/json" } });
};
