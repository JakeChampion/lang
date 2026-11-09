import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { llmsFull } from "../lib/llms.mjs";

export const GET: APIRoute = async ({ site }) => {
  const docs = await getCollection("docs");
  return new Response(llmsFull(site?.origin ?? "", docs), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
};
