const cairnReleaseOrigin = "https://github.com";
const cairnReleasePathPattern =
  /^\/RCooLeR\/Cairn\/releases\/tag\/(v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$/;

/**
 * Return the canonical external URL for a stable Cairn GitHub release.
 *
 * App-update metadata crosses the native/runtime boundary, so callers must not
 * pass its URL directly to the host browser. Keeping the accepted surface to
 * this repository's stable release-tag pages also excludes GitHub redirects,
 * downloads, and unrelated repository paths.
 */
export function normalizeCairnReleaseURL(value: unknown): string | null {
  if (typeof value !== "string") {
    return null;
  }

  const input = value.trim();
  if (!input) {
    return null;
  }

  let parsed: URL;
  try {
    parsed = new URL(input);
  } catch {
    return null;
  }

  if (
    parsed.protocol !== "https:" ||
    parsed.hostname !== "github.com" ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.port !== "" ||
    parsed.search !== "" ||
    parsed.hash !== ""
  ) {
    return null;
  }

  const releasePath = cairnReleasePathPattern.exec(parsed.pathname);
  if (!releasePath) {
    return null;
  }

  return `${cairnReleaseOrigin}/RCooLeR/Cairn/releases/tag/${releasePath[1]}`;
}
