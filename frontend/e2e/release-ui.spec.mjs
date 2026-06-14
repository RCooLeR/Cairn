import { AxeBuilder } from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import pixelmatch from 'pixelmatch';
import { PNG } from 'pngjs';

const routes = [
  { label: 'Overview', heading: 'Overview', slug: 'overview' },
  { label: 'Projects', heading: 'Projects', slug: 'projects' },
  { label: 'Updates', heading: 'Updates', slug: 'updates' },
  { label: 'Containers', heading: 'Containers', slug: 'containers' },
  { label: 'Images', heading: 'Images', slug: 'images' },
  { label: 'Volumes', heading: 'Volumes', slug: 'volumes' },
  { label: 'Networks', heading: 'Networks', slug: 'networks' },
  { label: 'Logs', heading: 'Logs', slug: 'logs' },
  { label: 'Terminal', heading: 'Terminal', slug: 'terminal' },
  { label: 'Settings', heading: 'Settings', slug: 'settings' },
];
const visualThreshold = 0.002;
const updateVisuals = process.env.CAIRN_UPDATE_VISUALS === '1';
const visualPlatform = process.env.CAIRN_VISUAL_PLATFORM || process.platform;
const goldenDir = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  'goldens',
  'release-ui',
  `chromium-${visualPlatform}-light`,
);

test.describe('release UI validation', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await disableMotion(page);
    await expect(page.getByRole('img', { name: 'Cairn' })).toBeVisible();
    await expect(
      page.getByRole('heading', { name: 'Overview', level: 1 }),
    ).toBeVisible();
  });

  for (const route of routes) {
    test(`${route.label} route has no serious axe violations`, async ({
      page,
    }) => {
      await openRoute(page, route);
      await assertNoSeriousAxeViolations(page, route.label);
    });
  }

  test('modal and popover states have no serious axe violations', async ({
    page,
  }) => {
    await page.keyboard.press('Control+K');
    await expect(page.getByRole('dialog')).toBeVisible();
    await assertNoSeriousAxeViolations(page, 'Command palette');
    await page.keyboard.press('Escape');

    await page.getByRole('button', { name: /^Notifications/ }).click();
    await expect(
      page.getByLabel('Notification center', { exact: true }),
    ).toBeVisible();
    await assertNoSeriousAxeViolations(page, 'Notification center');
    await page.getByRole('button', { name: /^Notifications/ }).click();
    await expect(
      page.getByLabel('Notification center', { exact: true }),
    ).toBeHidden();

    await page
      .getByRole('button', { name: /Import Project/i })
      .first()
      .click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await assertNoSeriousAxeViolations(page, 'Import Project modal');
  });

  test('route screenshots are visually stable', async ({ page }) => {
    for (const route of routes) {
      await openRoute(page, route);
      await assertScreenshotStable(page, route.label);
    }
  });

  test('route screenshots match committed goldens', async ({
    page,
  }, testInfo) => {
    for (const route of routes) {
      await openRoute(page, route);
      await assertMatchesGolden(page, route, testInfo);
    }
  });
});

test('daemon-stopped fixture renders degraded stale mode safely on every route', async ({
  page,
}) => {
  await page.addInitScript(() => {
    window.localStorage.setItem('cairn.release.fixture', 'degraded');
  });

  await page.goto('/');
  await disableMotion(page);
  await expect(page.getByRole('img', { name: 'Cairn' })).toBeVisible();
  await expect(page.getByText('Docker is not reachable')).toBeVisible();
  await expect(page.getByText('Stale cached data')).toBeVisible();

  for (const route of routes) {
    await openRoute(page, route);
    await expect(page.getByText('Docker is not reachable')).toBeVisible();
    await expect(page.getByText('Stale cached data')).toBeVisible();
    await assertNoSeriousAxeViolations(page, `Degraded ${route.label}`);
  }

  await openRoute(page, {
    label: 'Containers',
    heading: 'Containers',
    slug: 'containers',
  });
  await expect(page.getByRole('button', { name: 'Stop web' })).toBeDisabled();

  await openRoute(page, { label: 'Logs', heading: 'Logs', slug: 'logs' });

  const calls = await page.evaluate(
    () => window.__cairnReleaseMockCalls ?? {},
  );
  expect(calls['DockerService.StopContainer'] ?? 0).toBe(0);
  expect(calls['LogsService.StartLogStream'] ?? 0).toBe(0);
  expect(calls['MetricsService.StartStatsStream'] ?? 0).toBe(0);
});

async function openRoute(page, route) {
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  await nav
    .getByRole('button', { name: new RegExp(`^${escapeRegExp(route.label)}\\b`) })
    .click();
  await expect(
    page.getByRole('heading', { name: route.heading, level: 1 }),
  ).toBeVisible();
  await page.waitForLoadState('networkidle');
}

async function disableMotion(page) {
  await page.addStyleTag({
    content: `
      *,
      *::before,
      *::after {
        animation-delay: 0s !important;
        animation-duration: 0s !important;
        caret-color: transparent !important;
        scroll-behavior: auto !important;
        transition-delay: 0s !important;
        transition-duration: 0s !important;
      }
    `,
  });
}

async function assertNoSeriousAxeViolations(page, label) {
  const results = await new AxeBuilder({ page })
    .disableRules(['color-contrast'])
    .analyze();
  const violations = results.violations
    .filter((violation) => ['critical', 'serious'].includes(violation.impact))
    .map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      label,
      targets: violation.nodes.flatMap((node) => node.target),
    }));

  expect(violations).toEqual([]);
}

async function assertScreenshotStable(page, label) {
  const first = await page.screenshot({
    animations: 'disabled',
    fullPage: true,
  });
  await page.waitForTimeout(150);
  const second = await page.screenshot({
    animations: 'disabled',
    fullPage: true,
  });
  const firstImage = PNG.sync.read(first);
  const secondImage = PNG.sync.read(second);
  expect(firstImage.width, `${label} screenshot width changed`).toBe(
    secondImage.width,
  );
  expect(firstImage.height, `${label} screenshot height changed`).toBe(
    secondImage.height,
  );

  const diff = new PNG({ width: firstImage.width, height: firstImage.height });
  const changedPixels = pixelmatch(
    firstImage.data,
    secondImage.data,
    diff.data,
    firstImage.width,
    firstImage.height,
    { threshold: 0.1 },
  );
  const ratio = changedPixels / (firstImage.width * firstImage.height);
  expect(ratio, `${label} changed pixel ratio`).toBeLessThanOrEqual(
    visualThreshold,
  );
}

async function assertMatchesGolden(page, route, testInfo) {
  const actual = await page.screenshot({
    animations: 'disabled',
    fullPage: true,
  });
  const goldenPath = path.join(goldenDir, `${route.slug}.png`);

  if (updateVisuals) {
    fs.mkdirSync(goldenDir, { recursive: true });
    fs.writeFileSync(goldenPath, actual);
    testInfo.annotations.push({
      type: 'visual-baseline',
      description: `Updated ${path.relative(process.cwd(), goldenPath)}`,
    });
    return;
  }

  expect(
    fs.existsSync(goldenPath),
    `${route.label} golden missing; run CAIRN_UPDATE_VISUALS=1 npm run test:release-ui`,
  ).toBe(true);

  const expected = fs.readFileSync(goldenPath);
  const expectedImage = PNG.sync.read(expected);
  const actualImage = PNG.sync.read(actual);
  expect(actualImage.width, `${route.label} screenshot width changed`).toBe(
    expectedImage.width,
  );
  expect(actualImage.height, `${route.label} screenshot height changed`).toBe(
    expectedImage.height,
  );

  const diff = new PNG({
    width: expectedImage.width,
    height: expectedImage.height,
  });
  const changedPixels = pixelmatch(
    expectedImage.data,
    actualImage.data,
    diff.data,
    expectedImage.width,
    expectedImage.height,
    { threshold: 0.1 },
  );
  const ratio = changedPixels / (expectedImage.width * expectedImage.height);
  if (ratio > visualThreshold) {
    fs.writeFileSync(testInfo.outputPath(`${route.slug}-actual.png`), actual);
    fs.writeFileSync(
      testInfo.outputPath(`${route.slug}-expected.png`),
      expected,
    );
    fs.writeFileSync(
      testInfo.outputPath(`${route.slug}-diff.png`),
      PNG.sync.write(diff),
    );
  }
  expect(ratio, `${route.label} changed pixel ratio`).toBeLessThanOrEqual(
    visualThreshold,
  );
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
