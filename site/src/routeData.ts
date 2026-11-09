import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

// Each page except the home page has its own social card, made by
// src/pages/og/[...slug].png.ts. The home page keeps public/og.png, and
// Starlight's 404 page has no card.
export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;
  const id = route.entry.id;
  if (id === "" || id === "index" || id === "404") return;
  const image = `${context.site?.origin ?? ""}/og/${id}.png`;
  for (const tag of route.head) {
    if (tag.tag !== "meta") continue;
    if (tag.attrs?.property === "og:image" || tag.attrs?.name === "twitter:image") tag.attrs.content = image;
    if (tag.attrs?.property === "og:image:alt") tag.attrs.content = route.entry.data.title;
  }
});
