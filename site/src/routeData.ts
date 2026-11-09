import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

const base = import.meta.env.BASE_URL.replace(/\/$/, "");

// A build for another host sets CANONICAL_SITE to the origin of the main
// site. Each page then names the same page on that origin as canonical, so
// search engines list one copy of the site.
const canonicalSite = process.env.CANONICAL_SITE?.replace(/\/$/, "");

export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;

  // Starlight writes a hero action's link into the href as it is, without
  // the site base. The hero links in frontmatter are therefore site paths
  // such as `/tutorial/install/`, and this adds the base, the same as for
  // sidebar links.
  const hero = route.entry.data.hero;
  if (hero?.actions) {
    hero.actions = hero.actions.map((action) =>
      action.link.startsWith("/") && !action.link.startsWith("//")
        ? { ...action, link: `${base}${action.link}` }
        : action,
    );
  }

  // Each page except the home page has its own social card, made by
  // src/pages/og/[...slug].png.ts.
  // The home page keeps public/og.png, and Starlight's 404 page has no card.
  const id = route.entry.id;
  if (id !== "" && id !== "index" && id !== "404") {
    const origin = canonicalSite ?? (context.site?.origin ?? "");
    const prefix = canonicalSite ? "" : base;
    const image = `${origin}${prefix}/og/${id}.png`;
    for (const tag of route.head) {
      if (tag.tag !== "meta") continue;
      if (tag.attrs?.property === "og:image" || tag.attrs?.name === "twitter:image") tag.attrs.content = image;
      if (tag.attrs?.property === "og:image:alt") tag.attrs.content = route.entry.data.title;
    }
  }

  if (canonicalSite) {
    const path = context.url.pathname.startsWith(`${base}/`)
      ? context.url.pathname.slice(base.length)
      : context.url.pathname;
    const href = `${canonicalSite}${path}`;
    for (const tag of route.head) {
      if (tag.tag === "link" && tag.attrs?.rel === "canonical") tag.attrs.href = href;
      if (tag.tag === "meta" && tag.attrs?.property === "og:url") tag.attrs.content = href;
    }
  }
});
