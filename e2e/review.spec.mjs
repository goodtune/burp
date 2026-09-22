import { test, expect } from '@playwright/test';

const fake = 'http://localhost:9999';
const prPayload = () => JSON.stringify({
  action: 'synchronize', number: 5,
  pull_request: { number: 5, user: { login: 'alice' }, head: { sha: 'HEAD2' } },
  repository: { name: 'api', full_name: 'acme/api', owner: { login: 'acme' } },
});

test.describe.configure({ mode: 'serial' });

let page;
const errors = [];

test.beforeAll(async ({ browser }) => {
  await fetch(fake + '/reset');
  page = await browser.newPage({ viewport: { width: 1400, height: 1000 } });
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
});

test.afterAll(async () => {
  await page.close();
});

test('sign in through the GitHub App OAuth flow', async () => {
  await page.goto('/');
  await page.getByText('Sign in with GitHub').click();
  await page.waitForURL('**/inbox');
});

test('inbox sections stream in and the filter re-queries', async () => {
  await expect(page.getByText('Add rate limiting to /tokens')).toBeVisible();
  await expect(page.getByText('Needs your review')).toBeVisible();
  await page.fill('input[type=search]', 'repo:acme/api');
  await expect(page.getByText('filter: repo:acme/api')).toBeVisible();
});

test('review page shows the action card, checks and the diff', async () => {
  await page.getByRole('link', { name: 'Add rate limiting to /tokens' }).click();
  await page.waitForURL('**/pr/acme/api/5');
  await expect(page.locator('.action', { hasText: 'Checks failing' })).toBeVisible();
  await expect(page.locator('#pr-diff').getByText('# Config')).toBeVisible();
  await page.locator('#pr-files .path', { hasText: 'ratelimit.go' }).click();
  await expect(page.locator('#pr-diff').getByText('package proxy')).toBeVisible();
  await expect(page.locator('.thread', { hasText: 'golang.org/x/time/rate' })).toBeVisible();
});

test('clicking a line number opens the composer and saves a draft', async () => {
  await page.locator('#pr-diff tr.row').nth(1).locator('td.no').nth(1).click();
  const composer = page.locator('#pr-diff .composer textarea');
  await expect(composer).toBeVisible();
  await composer.fill('Should this be exported?');
  await page.getByRole('button', { name: 'Add to review' }).click();
  await expect(page.locator('.draft .body', { hasText: 'Should this be exported?' })).toBeVisible();
  await expect(page.locator('.draft .draft-head')).toContainText('line 2 (right)');
  await expect(page.locator('#pr-toolbar .badge', { hasText: '1 draft' })).toBeVisible();
});

test('reviewed marks and keyboard navigation', async () => {
  await page.locator('#pr-files li.file').first().locator('input[type=checkbox]').check();
  await expect(page.locator('#pr-files .files-head b')).toHaveText('1/2');
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('k');
  await expect(page.locator('#pr-diff .diff-head b')).toHaveText('docs/config.md');
  await page.keyboard.press('v');
  await expect(page.locator('table.diff.split')).toBeVisible();
  await page.keyboard.press('v');
  await expect(page.locator('table.diff.unified')).toBeVisible();
});

test('revision picker compares two commits', async () => {
  await page.locator('#pr-toolbar select').first().selectOption('C1');
  await expect(page.locator('#pr-diff td.code', { hasText: 'y' })).toBeVisible();
  await page.locator('#pr-toolbar select').first().selectOption('');
  await expect(page.locator('#pr-diff .diff-head', { hasText: 'docs/config.md' })).toBeVisible();
});

test('reply to and resolve a thread', async () => {
  await page.locator('#pr-files .path', { hasText: 'ratelimit.go' }).click();
  const thread = page.locator('.thread').first();
  await expect(thread).toBeVisible();
  await thread.getByRole('button', { name: 'Reply' }).click();
  await thread.locator('textarea').fill('done in r2');
  await thread.getByRole('button', { name: 'Post reply' }).click();
  await expect.poll(async () => (await (await fetch(fake + '/state')).text())).toContain('replies=1');
  await page.locator('.thread').first().getByRole('button', { name: 'Resolve' }).click();
  await expect.poll(async () => (await (await fetch(fake + '/state')).text())).toContain('resolved=1');
});

test('submit the review with the batched draft', async () => {
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('a');
  const dialog = page.locator('.dialog-box', { hasText: 'Submit review' });
  await expect(dialog).toBeVisible();
  await dialog.locator('textarea').fill('Looks good overall');
  await dialog.getByRole('button', { name: 'Submit review' }).click();
  await expect(page.getByText('Review submitted (approve)')).toBeVisible();
  const state = await (await fetch(fake + '/state')).text();
  expect(state).toContain('"event":"APPROVE"');
  expect(state).toContain('"body":"Should this be exported?"');
  expect(state).toContain('"line":2');
});

test('a webhook for new commits shows the banner without changing the diff', async () => {
  await fetch(fake + '/push');
  const r = await fetch('http://localhost:8080/webhooks/github', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request' },
    body: prPayload(),
  });
  expect(r.status).toBe(202);
  await expect(page.getByText('New commits were pushed')).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('#pr-diff').getByText('package proxy')).toBeVisible();
  await page.getByRole('button', { name: 'Load the latest revision' }).click();
  await expect(page.locator('#pr-toolbar select').nth(1)).toContainText('HEAD2');
});

test('help dialog and settings', async () => {
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('?');
  await expect(page.getByText('Keyboard shortcuts')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByText('Keyboard shortcuts')).toBeHidden();
  await page.goto('/settings');
  await expect(page.getByText('acme')).toBeVisible();
});

test('no browser errors were logged', async () => {
  expect(errors).toEqual([]);
});
