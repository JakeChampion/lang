import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { llmsFull } from "../lib/llms.mjs";

export const GET: APIRoute = async ({ site }) => {
  const origin = (process.env.CANONICAL_SITE ?? site?.origin ?? "").replace(/\/$/, "");
  const base = process.env.CANONICAL_SITE ? "" : import.meta.env.BASE_URL.replace(/\/$/, "");
  const docs = await getCollection("docs");
  return new Response(llmsFull(origin, base, docs), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
};
