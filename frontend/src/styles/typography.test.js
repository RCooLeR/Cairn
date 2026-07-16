import { readFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

import { describe, expect, it } from "vitest";

const frontendRoot = process.cwd();
const applicationCSS = readFileSync(
  path.join(frontendRoot, "src/styles/index.css"),
  "utf8",
);
const loaderCSS = readFileSync(
  path.join(frontendRoot, "src/styles/cairn-loader.css"),
  "utf8",
);

describe("typography configuration", () => {
  it("uses system families that provide the requested weight hierarchy", () => {
    expect(applicationCSS).toMatch(
      /font-family:\s*ui-sans-serif,\s*system-ui,/,
    );
    expect(loaderCSS).toMatch(/font-family:\s*ui-sans-serif,\s*system-ui,/);
  });

  it("does not advertise the static Inter Medium file as a variable range", () => {
    expect(applicationCSS).not.toContain("Inter-Medium.ttf");
    expect(loaderCSS).not.toContain("Inter-Medium.ttf");
  });
});
