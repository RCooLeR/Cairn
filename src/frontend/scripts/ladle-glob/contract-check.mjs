import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { after, before, test } from "node:test";
import { pathToFileURL } from "node:url";

// Exercise the installed override from Ladle's resolver, not the source module.
const requireFromLadle = createRequire(
  import.meta.resolve("@ladle/react/build"),
);
const { globby } = await import(
  pathToFileURL(requireFromLadle.resolve("globby"))
);

const previousCwd = process.cwd();
let root;
let fileSymlinksAvailable = true;
const visible = [
  "src/A.stories.tsx",
  "src/ignore/D.stories.tsx",
  "src/nested/B.stories.tsx",
];

before(async () => {
  root = await mkdtemp(path.join(os.tmpdir(), "cairn-ladle-glob-"));
  for (const file of [
    ...visible,
    "src/.Hidden.stories.tsx",
    "src/.hidden/C.stories.tsx",
    "src/nested/Other.txt",
    "outside/External.stories.tsx",
  ]) {
    await mkdir(path.dirname(path.join(root, file)), { recursive: true });
    await writeFile(path.join(root, file), "");
  }
  await mkdir(path.join(root, "src/Directory.stories.tsx"));
  await symlink(
    path.join(root, "outside"),
    path.join(root, "src/linked"),
    process.platform === "win32" ? "junction" : "dir",
  );
  try {
    await symlink(
      path.join(root, "outside/External.stories.tsx"),
      path.join(root, "src/Link.stories.tsx"),
      "file",
    );
  } catch (error) {
    if (process.platform !== "win32" || error.code !== "EPERM") throw error;
    fileSymlinksAvailable = false;
  }
  process.chdir(root);
});

after(async () => {
  process.chdir(previousCwd);
  if (root) {
    assert.ok(
      path.resolve(root).startsWith(path.resolve(os.tmpdir()) + path.sep),
    );
    await rm(root, { recursive: true, force: true });
  }
});

const expectedStories = () =>
  [
    ...visible,
    "src/linked/External.stories.tsx",
    ...(fileSymlinksAvailable ? ["src/Link.stories.tsx"] : []),
  ].sort();
const absolute = (file) => path.join(root, file).split(path.sep).join("/");

test("discovers Cairn's story pattern, excludes directories and implicit dot paths", async () => {
  assert.deepEqual(await globby("src/**/*.stories.tsx"), expectedStories());
});

test("deduplicates multiple patterns and preserves exclusion ordering", async () => {
  const expected = expectedStories().filter(
    (file) => !file.includes("/ignore/"),
  );
  assert.deepEqual(
    await globby([
      "src/**/*.stories.tsx",
      "!src/ignore/**",
      "src/A.stories.tsx",
    ]),
    expected,
  );
  assert.deepEqual(
    await globby(["!src/ignore/**", "src/**/*.stories.tsx"]),
    expectedStories(),
  );
  assert.deepEqual(
    await globby([
      "src/**/*.stories.tsx",
      "!src/ignore/**",
      "src/ignore/D.stories.tsx",
    ]),
    expectedStories(),
  );
});

test("supports absolute patterns and absolute exclusions", async () => {
  assert.deepEqual(
    await globby([
      absolute("src/**/*.stories.tsx"),
      `!${absolute("src/ignore/**")}`,
    ]),
    expectedStories()
      .filter((file) => !file.includes("/ignore/"))
      .map(absolute),
  );
  assert.deepEqual(
    await globby([absolute("src/**/*.stories.tsx"), "!src/ignore/**"]),
    expectedStories().map(absolute),
  );
  assert.deepEqual(
    await globby(["src/**/*.stories.tsx", `!${absolute("src/ignore/**")}`]),
    expectedStories().filter((file) => !file.includes("/ignore/")),
  );
});

test("accepts explicit dot paths and follows linked directories", async () => {
  assert.deepEqual(await globby("src/.hidden/**/*.stories.tsx"), [
    "src/.hidden/C.stories.tsx",
  ]);
  assert.deepEqual(await globby("src/.*.stories.tsx"), [
    "src/.Hidden.stories.tsx",
  ]);
  assert.deepEqual(await globby("src/linked/**/*.stories.tsx"), [
    "src/linked/External.stories.tsx",
  ]);
});

test("supports directory expansion, brace patterns, empty and unmatched patterns", async () => {
  assert.deepEqual(await globby("src/nested"), [
    "src/nested/B.stories.tsx",
    "src/nested/Other.txt",
  ]);
  assert.deepEqual(await globby("src/{A,missing}.stories.tsx"), [
    "src/A.stories.tsx",
  ]);
  assert.deepEqual(await globby([]), []);
  assert.deepEqual(await globby("missing/**/*.stories.tsx"), []);
  assert.deepEqual(await globby(["!src/**"]), []);
});

test("fails clearly if Ladle starts using options or invalid patterns", async () => {
  await assert.rejects(
    globby("src/**", { dot: true }),
    /does not support options/,
  );
  await assert.rejects(globby(["src/**", 42]), /non-empty strings/);
  await assert.rejects(globby(""), /non-empty strings/);
});
