import { existsSync } from "node:fs";
import { join } from "node:path";

import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

import { moduleSource } from "./lib/stdlib-index.mjs";

const repo = "https://github.com/JakeChampion/lang";

// Each page except the home page has its own social card, made by
// src/pages/og/[...slug].png.ts. The home page keeps public/og.png, and
// Starlight's 404 page has no card.
export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;
  const id = route.entry.id;

  // `fern -doc` generates a stdlib page from a module, so the page's edit
  // link names the module's source, not the generated file. The build runs
  // in site/, so the repository root is one directory up.
  if (id.startsWith("stdlib/")) {
    const file = moduleSource(route.entry.data.title, (p) => existsSync(join(process.cwd(), "..", p)));
    route.editUrl = file ? new URL(`${repo}/edit/main/${file}`) : undefined;
  }

  if (id === "" || id === "index" || id === "404") return;
  const image = `${context.site?.origin ?? ""}/og/${id}.png`;
  for (const tag of route.head) {
    if (tag.tag !== "meta") continue;
    if (tag.attrs?.property === "og:image" || tag.attrs?.name === "twitter:image") tag.attrs.content = image;
    if (tag.attrs?.property === "og:image:alt") tag.attrs.content = route.entry.data.title;
  }
});
