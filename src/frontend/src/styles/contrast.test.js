import { readFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

import { describe, expect, it } from "vitest";

const frontendRoot = process.cwd();
const themeCSS = readFileSync(
  path.join(frontendRoot, "src/styles/index.css"),
  "utf8",
);

const themeTokensPattern = new RegExp(
  [
    "--bg-app:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--bg-panel:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--bg-card:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--bg-inset:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--text-secondary:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--text-muted:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
    "[\\s\\S]*?--focus-ring:\\s*(\\d+)\\s+(\\d+)\\s+(\\d+);",
  ].join(""),
  "g",
);

function rgb(values) {
  return values.map(Number);
}

function relativeLuminance([red, green, blue]) {
  const [r, g, b] = [red, green, blue].map((channel) => {
    const value = channel / 255;
    return value <= 0.04045
      ? value / 12.92
      : Math.pow((value + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrastRatio(foreground, background) {
  const foregroundLuminance = relativeLuminance(foreground);
  const backgroundLuminance = relativeLuminance(background);
  return (
    (Math.max(foregroundLuminance, backgroundLuminance) + 0.05) /
    (Math.min(foregroundLuminance, backgroundLuminance) + 0.05)
  );
}

describe("semantic text contrast", () => {
  it("keeps secondary and muted text opaque", () => {
    for (const token of ["text-secondary", "text-muted"]) {
      expect(themeCSS).toContain(`--color-${token}: rgb(var(--${token}) / 1);`);
    }
  });

  it("meets WCAG AA on every supported application surface", () => {
    const tokenSets = Array.from(themeCSS.matchAll(themeTokensPattern));
    expect(tokenSets).toHaveLength(3);

    for (const [themeIndex, match] of tokenSets.entries()) {
      const surfaces = [
        rgb(match.slice(1, 4)),
        rgb(match.slice(4, 7)),
        rgb(match.slice(7, 10)),
        rgb(match.slice(10, 13)),
      ];
      const textColors = [rgb(match.slice(13, 16)), rgb(match.slice(16, 19))];
      const focusRing = rgb(match.slice(19, 22));

      for (const foreground of textColors) {
        for (const background of surfaces) {
          expect(
            contrastRatio(foreground, background),
            `theme ${themeIndex} foreground ${foreground.join(" ")} on ${background.join(" ")}`,
          ).toBeGreaterThanOrEqual(4.5);
        }
      }
      for (const background of surfaces) {
        expect(
          contrastRatio(focusRing, background),
          `theme ${themeIndex} focus ring ${focusRing.join(" ")} on ${background.join(" ")}`,
        ).toBeGreaterThanOrEqual(3);
      }
    }
  });
});
