import { glob, stat } from "node:fs/promises";
import path from "node:path";

async function expandDirectory(pattern) {
  try {
    if ((await stat(pattern)).isDirectory()) {
      return `${pattern.replace(/\/$/, "")}/**/*`;
    }
  } catch (error) {
    if (error.code !== "ENOENT" && error.code !== "ENOTDIR") throw error;
  }
  return pattern;
}

// Ladle 5.1.1 uses only globby(storyPatterns), without options, at six call sites.
// Keep this adapter scoped to Ladle; it is not a replacement for the full API.
export async function globby(patterns, options) {
  if (options !== undefined) {
    throw new TypeError("Cairn's Ladle glob adapter does not support options");
  }
  const list = typeof patterns === "string" ? [patterns] : patterns;
  if (
    !Array.isArray(list) ||
    list.some((pattern) => typeof pattern !== "string" || pattern.length === 0)
  ) {
    throw new TypeError("Ladle story patterns must be non-empty strings");
  }
  const entries = [];
  for (const pattern of list) {
    const negative = pattern.startsWith("!") && !pattern.startsWith("!(");
    entries.push({
      negative,
      pattern: await expandDirectory(negative ? pattern.slice(1) : pattern),
    });
  }
  const files = new Set();
  for (const [index, { pattern, negative }] of entries.entries()) {
    if (negative) continue;
    // Exclusions affect earlier positives; later positives can re-include files.
    const exclusions = entries
      .slice(index + 1)
      .filter((entry) => entry.negative)
      .map((entry) => entry.pattern);
    // Globby does not apply relative exclusions to absolute positive patterns.
    const exclude = path.isAbsolute(pattern)
      ? exclusions.filter((entry) => path.isAbsolute(entry))
      : exclusions;
    for await (const file of glob(pattern, { exclude, followSymlinks: true })) {
      try {
        if ((await stat(file)).isFile())
          files.add(file.split(path.sep).join("/"));
      } catch (error) {
        if (error.code !== "ENOENT" && error.code !== "ENOTDIR") throw error;
      }
    }
  }
  return [...files].sort();
}
