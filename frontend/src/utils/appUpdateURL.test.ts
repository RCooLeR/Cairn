import { describe, expect, it } from "vitest";

import { normalizeCairnReleaseURL } from "./appUpdateURL";

describe("normalizeCairnReleaseURL", () => {
  it.each([
    [
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0",
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0",
    ],
    [
      "  https://GITHUB.com:443/RCooLeR/Cairn/releases/tag/v12.34.56  ",
      "https://github.com/RCooLeR/Cairn/releases/tag/v12.34.56",
    ],
  ])("accepts and canonicalizes a Cairn release URL", (input, expected) => {
    expect(normalizeCairnReleaseURL(input)).toBe(expected);
  });

  it.each([
    ["an empty value", ""],
    ["a non-string value", null],
    ["a malformed URL", "not a url"],
    ["JavaScript", "javascript:alert(1)"],
    ["a file URL", "file:///tmp/cairn"],
    ["unencrypted HTTP", "http://github.com/RCooLeR/Cairn/releases/tag/v1.0.0"],
    [
      "credentials",
      "https://user:secret@github.com/RCooLeR/Cairn/releases/tag/v1.0.0",
    ],
    [
      "a nonstandard port",
      "https://github.com:8443/RCooLeR/Cairn/releases/tag/v1.0.0",
    ],
    [
      "a lookalike host",
      "https://github.com.example.test/RCooLeR/Cairn/releases/tag/v1.0.0",
    ],
    [
      "a GitHub subdomain",
      "https://www.github.com/RCooLeR/Cairn/releases/tag/v1.0.0",
    ],
    [
      "another repository",
      "https://github.com/RCooLeR/Cairn-Desktop/releases/tag/v1.0.0",
    ],
    [
      "different path casing",
      "https://github.com/rcooler/cairn/releases/tag/v1.0.0",
    ],
    [
      "a release asset",
      "https://github.com/RCooLeR/Cairn/releases/download/v1.0.0/Cairn.exe",
    ],
    ["a non-release page", "https://github.com/RCooLeR/Cairn/issues/1"],
    [
      "an encoded path segment",
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0%2Fevil",
    ],
    [
      "a non-semver tag",
      "https://github.com/RCooLeR/Cairn/releases/tag/latest",
    ],
    [
      "a numeric tag with a leading zero",
      "https://github.com/RCooLeR/Cairn/releases/tag/v01.2.3",
    ],
    [
      "a trailing path separator",
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0/",
    ],
    [
      "a query string",
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0?source=update",
    ],
    [
      "a fragment",
      "https://github.com/RCooLeR/Cairn/releases/tag/v1.0.0#assets",
    ],
  ])("rejects %s", (_description, input) => {
    expect(normalizeCairnReleaseURL(input)).toBeNull();
  });
});
