import { AxeBuilder } from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import pixelmatch from 'pixelmatch';
import { PNG } from 'pngjs';

const routes = [
  { label: 'Overview', heading: 'Overview' },
  { label: 'Projects', heading: 'Projects' },
  { label: 'Updates', heading: 'Updates' },
  { label: 'Containers', heading: 'Containers' },
  { label: 'Images', heading: 'Images' },
  { label: 'Volumes', heading: 'Volumes' },
  { label: 'Networks', heading: 'Networks' },
  { label: 'Logs', heading: 'Logs' },
  { label: 'Terminal', heading: 'Terminal' },
  { label: 'Settings', heading: 'Settings' },
];

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
  expect(ratio, `${label} changed pixel ratio`).toBeLessThanOrEqual(0.002);
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
