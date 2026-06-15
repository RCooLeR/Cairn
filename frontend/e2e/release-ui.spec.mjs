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
const firstRenderBudgetMs = 1500;
const routeSwitchBudgetMs = 200;
const filterBudgetMs = 100;
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

  test('overflowing routes keep page content scrollable', async ({ page }) => {
    await page.setViewportSize({ width: 1260, height: 720 });

    for (const route of routes) {
      await openRoute(page, route);
      await assertScrollRegionCanReachOverflow(page, route.label);
    }

    await openRoute(page, {
      label: 'Settings',
      heading: 'Settings',
      slug: 'settings',
    });
    const settingsScroll = await scrollRegionMetrics(page);
    expect(
      settingsScroll.scrollHeight,
      'Settings should overflow in the compact release viewport',
    ).toBeGreaterThan(settingsScroll.clientHeight);
    expect(
      settingsScroll.afterScrollTop,
      'Settings content could not scroll to lower provider controls',
    ).toBeGreaterThan(0);
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

  const calls = await page.evaluate(() => window.__cairnReleaseMockCalls ?? {});
  expect(calls['DockerService.StopContainer'] ?? 0).toBe(0);
  expect(calls['LogsService.StartLogStream'] ?? 0).toBe(0);
  expect(calls['MetricsService.StartStatsStream'] ?? 0).toBe(0);
});

test('seed-scale fixture meets release responsiveness budgets', async ({
  page,
}, testInfo) => {
  await page.addInitScript(() => {
    window.localStorage.setItem('cairn.release.fixture', 'seeded');
  });

  await page.goto('/');
  await disableMotion(page);
  await expect(page.getByRole('img', { name: 'Cairn' })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Overview', level: 1 }),
  ).toBeVisible();
  await expect(page.getByLabel('Docker object counts')).toContainText('100');

  const firstRenderMs = await page.evaluate(() => performance.now());
  annotatePerf(
    testInfo,
    'seed dashboard first meaningful render',
    firstRenderMs,
  );
  expect(
    firstRenderMs,
    `seed dashboard first meaningful render took ${firstRenderMs.toFixed(1)}ms`,
  ).toBeLessThanOrEqual(firstRenderBudgetMs);

  for (const label of [
    'Projects',
    'Containers',
    'Images',
    'Volumes',
    'Networks',
  ]) {
    const route = routes.find((candidate) => candidate.label === label);
    const elapsed = await openRouteForBudget(page, route);
    annotatePerf(testInfo, `${label} route switch`, elapsed);
    expect(
      elapsed,
      `${label} route switch took ${elapsed.toFixed(1)}ms`,
    ).toBeLessThanOrEqual(routeSwitchBudgetMs);
  }

  await openRoute(page, {
    label: 'Containers',
    heading: 'Containers',
    slug: 'containers',
  });
  const filterMs = await fillInputAndMeasureFrame(
    page,
    'Search inventory',
    'service-042',
  );
  annotatePerf(testInfo, 'container inventory filter', filterMs);
  expect(
    filterMs,
    `container inventory filter took ${filterMs.toFixed(1)}ms`,
  ).toBeLessThanOrEqual(filterBudgetMs);
  await expect(page.getByText('service-042').first()).toBeVisible();

  const logsElapsed = await openRouteForBudget(page, {
    label: 'Logs',
    heading: 'Logs',
    slug: 'logs',
  });
  annotatePerf(testInfo, 'Logs route switch', logsElapsed);
  expect(
    logsElapsed,
    `Logs route switch took ${logsElapsed.toFixed(1)}ms`,
  ).toBeLessThanOrEqual(routeSwitchBudgetMs);

  await expect(page.getByText(/5[,. ]000 buffered/)).toBeVisible({
    timeout: 3000,
  });
  await expect(page.getByText(/5[,. ]000 visible lines/)).toBeVisible();

  const viewer = page.getByRole('log', { name: 'Log lines' });
  await expect(
    viewer.getByText('INFO release validation log line 4999'),
  ).toBeVisible();

  const renderedRows = await viewer.locator('div.absolute').count();
  annotatePerf(testInfo, 'rendered log rows', renderedRows, 'rows');
  expect(renderedRows).toBeLessThanOrEqual(80);
});

async function openRoute(page, route) {
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  await nav
    .getByRole('button', {
      name: new RegExp(`^${escapeRegExp(route.label)}\\b`),
    })
    .click();
  await expect(
    page.getByRole('heading', { name: route.heading, level: 1 }),
  ).toBeVisible();
  await page.waitForLoadState('networkidle');
}

async function openRouteForBudget(page, route) {
  return page.evaluate(async ({ heading, label }) => {
    const buttons = Array.from(document.querySelectorAll('nav button'));
    const button = buttons.find((item) =>
      item.textContent?.trim().startsWith(label),
    );
    if (!button) {
      throw new Error(`Missing navigation button for ${label}`);
    }

    const start = performance.now();
    button.click();
    for (let frame = 0; frame < 30; frame += 1) {
      await new Promise(requestAnimationFrame);
      const headingNode = Array.from(document.querySelectorAll('h1')).find(
        (node) => node.textContent?.trim() === heading,
      );
      if (headingNode) {
        return performance.now() - start;
      }
    }
    throw new Error(`Timed out waiting for ${heading}`);
  }, route);
}

async function fillInputAndMeasureFrame(page, label, value) {
  return page.evaluate(
    async ({ label: inputLabel, value: nextValue }) => {
      const input = Array.from(document.querySelectorAll('input')).find(
        (candidate) => candidate.getAttribute('aria-label') === inputLabel,
      );
      if (!(input instanceof HTMLInputElement)) {
        throw new Error(`Missing input with label ${inputLabel}`);
      }

      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value',
      )?.set;
      const start = performance.now();
      input.focus();
      valueSetter?.call(input, nextValue);
      input.dispatchEvent(
        new InputEvent('input', {
          bubbles: true,
          data: nextValue,
          inputType: 'insertText',
        }),
      );
      await new Promise(requestAnimationFrame);
      return performance.now() - start;
    },
    { label, value },
  );
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
      html[data-cairn-visual-fullpage],
      html[data-cairn-visual-fullpage] body,
      html[data-cairn-visual-fullpage] #root,
      html[data-cairn-visual-fullpage] main,
      html[data-cairn-visual-fullpage] main > div,
      html[data-cairn-visual-fullpage] main > div > section {
        height: auto !important;
        min-height: 960px !important;
        overflow: visible !important;
      }

      html[data-cairn-visual-fullpage] [data-testid="app-scroll-region"] {
        flex: none !important;
        height: auto !important;
        min-height: 0 !important;
        overflow: visible !important;
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

async function assertScrollRegionCanReachOverflow(page, label) {
  const metrics = await scrollRegionMetrics(page);
  expect(metrics.clientHeight, `${label} scroll region has no height`).toBeGreaterThan(0);
  if (metrics.scrollHeight > metrics.clientHeight + 1) {
    expect(
      metrics.afterScrollTop,
      `${label} content overflows but cannot be scrolled`,
    ).toBeGreaterThan(0);
  }
}

async function scrollRegionMetrics(page) {
  return page.getByTestId('app-scroll-region').evaluate((node) => {
    node.scrollTop = 0;
    const beforeScrollTop = node.scrollTop;
    node.scrollTop = node.scrollHeight;
    const afterScrollTop = node.scrollTop;
    return {
      afterScrollTop,
      beforeScrollTop,
      clientHeight: node.clientHeight,
      scrollHeight: node.scrollHeight,
    };
  });
}

async function assertScreenshotStable(page, label) {
  const first = await captureFullRouteScreenshot(page);
  await page.waitForTimeout(150);
  const second = await captureFullRouteScreenshot(page);
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
  const actual = await captureFullRouteScreenshot(page);
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

async function captureFullRouteScreenshot(page) {
  await page.getByTestId('app-scroll-region').evaluate((node) => {
    node.scrollTop = 0;
    document.documentElement.dataset.cairnVisualFullpage = 'true';
  });
  await page.waitForTimeout(0);
  try {
    return await page.screenshot({
      animations: 'disabled',
      fullPage: true,
    });
  } finally {
    await page.evaluate(() => {
      delete document.documentElement.dataset.cairnVisualFullpage;
    });
  }
}

function annotatePerf(testInfo, label, value, unit = 'ms') {
  testInfo.annotations.push({
    type: 'perf',
    description: `${label}: ${Number(value).toFixed(1)}${unit}`,
  });
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
