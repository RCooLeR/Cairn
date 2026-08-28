import { readdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const bindingsDirectory = fileURLToPath(
  new URL("../bindings/", import.meta.url),
);

async function trimTrailingWhitespace(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const entryPath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      await trimTrailingWhitespace(entryPath);
      continue;
    }
    if (!entry.isFile()) {
      continue;
    }

    const source = await readFile(entryPath, "utf8");
    const normalized = source.replace(/[\t ]+(?=\r?$)/gm, "");
    if (normalized !== source) {
      await writeFile(entryPath, normalized, "utf8");
    }
  }
}

await trimTrailingWhitespace(bindingsDirectory);
