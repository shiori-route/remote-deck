import { test, expect } from '@playwright/test';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtemp, chmod, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { createServer } from 'node:net';

let process: ChildProcess;
let base: string;
let directory: string;
let token: string;
let serviceLogs = '';
let secure = false;
let cookieName = 'remotedeck_session';

test.beforeAll(async ({}, testInfo) => {
  secure = testInfo.project.name.startsWith('https-');
  cookieName = secure ? '__Host-remotedeck_session' : 'remotedeck_session';
  directory = await mkdtemp(join(tmpdir(), 'remotedeck-browser-'));
  await chmod(directory, 0o700);
  const args = ['-addr', '127.0.0.1:0', '-data-dir', directory];
  if (secure) {
    const reservation = createServer();
    reservation.listen(0, '127.0.0.1');
    await once(reservation, 'listening');
    const address = reservation.address();
    if (!address || typeof address === 'string') throw new Error('missing port');
    const port = address.port;
    await new Promise<void>((resolve, reject) => reservation.close(err => err ? reject(err) : resolve()));
    const cert = join(directory, 'cert.pem');
    const key = join(directory, 'key.pem');
    execFileSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '1', '-subj', '/CN=RemoteDeck browser test', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', key, '-out', cert], { stdio: 'ignore' });
    await chmod(key, 0o600);
    args[1] = `127.0.0.1:${port}`;
    args.push('-origin', `https://127.0.0.1:${port}`, '-tls-cert', cert, '-tls-key', key);
  }
  process = spawn(globalThis.process.env.REMOTEDECK_BIN || resolve('../bin/remotedeck'), args, { stdio: ['ignore', 'ignore', 'pipe'] });
  const lines = createInterface({ input: process.stderr! });
  base = await new Promise<string>((resolveReady, reject) => {
    const timeout = setTimeout(() => reject(new Error('service readiness timed out')), 10_000);
    process.once('error', error => { clearTimeout(timeout); reject(error); });
    process.once('exit', () => { clearTimeout(timeout); reject(new Error('service exited')); });
    lines.on('line', line => {
      serviceLogs += line + '\n';
      try {
        const entry = JSON.parse(line);
        if (entry.msg === 'service listening') { clearTimeout(timeout); resolveReady(entry.origin); }
      } catch { /* Non-JSON diagnostics are retained for the leak check. */ }
    });
  });
  token = (await readFile(join(directory, 'auth.token'), 'utf8')).trim();
});

test.afterAll(async () => {
  if (process && process.exitCode === null) { process.kill('SIGTERM'); await once(process, 'exit'); }
  expect(serviceLogs.includes(token)).toBe(false);
});

test('login, submit, logs, resume, CSRF and logout', async ({ page, context }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', msg => { if (msg.type() === 'error' && !msg.text().includes('status of 401') && !msg.text().includes('status of 403')) errors.push(msg.text()); });
  await page.goto(base);
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  await page.getByLabel('访问令牌', { exact: true }).fill('invalid');
  await page.getByRole('button', { name: '进入控制台' }).click();
  await expect(page.getByRole('alert')).toContainText('令牌无效');
  await page.getByLabel('访问令牌', { exact: true }).fill(token);
  await page.getByRole('button', { name: '进入控制台' }).click();
  await expect(page.getByRole('heading', { name: '任务工作台' })).toBeVisible();
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0,0]);
  const cookies = await context.cookies();
  const session = cookies.find(cookie => cookie.name === cookieName);
  expect(session?.httpOnly).toBe(true);
  expect(session?.secure).toBe(secure);
  expect(session?.path).toBe(secure ? '/' : '/api/v1');
  if (secure) await expect(page.getByText('HTTPS 连接', { exact: true })).toBeVisible();
  expect(session?.sameSite).toBe('Strict');
  expect(await page.evaluate(() => document.cookie.includes('remotedeck_session'))).toBe(false);

  const denied = await context.request.post(base + '/api/v1/jobs', { data: { task_type: 'system-info', parameters: {} }, headers: { Origin: base } });
  expect(denied.status()).toBe(403);
  const crossOrigin = await context.request.get(base + '/api/v1/session', { headers: { Origin: 'http://127.0.0.1:9' } });
  expect(crossOrigin.status()).toBe(403);

  await page.getByRole('button', { name: '提交任务' }).click();
  await expect(page.getByRole('textbox', { name: '任务输出日志' })).toHaveValue(/Linux/);
  await expect(page.getByText('读取完成', { exact: false })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: '任务工作台' })).toBeVisible();
  const row = page.locator('.job-row').first();
  await expect(row).toContainText('已完成');
  await row.click();
  await expect(page.getByRole('textbox', { name: '任务输出日志' })).toHaveValue(/Linux/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: `/tmp/remotedeck-${testInfo.project.name}.png`, fullPage: true });

  const submissions = await Promise.all(Array.from({ length: 20 }, () => context.request.post(base + '/api/v1/jobs', {
    headers: { Authorization: `Bearer ${token}` }, data: { task_type: 'system-info', parameters: {} },
  })));
  expect(submissions.every(response => response.status() === 201)).toBe(true);
  await expect(page.locator('.job-row')).toHaveCount(20);
  await page.getByRole('button', { name: '下一页' }).click();
  await expect(page.getByText('第 2 页', { exact: true })).toBeVisible();
  await expect(page.locator('.job-row')).toHaveCount(1);
  await page.getByRole('button', { name: '上一页' }).click();
  await expect(page.locator('.job-row')).toHaveCount(20);

  // Output must remain literal text even when a job emits HTML.
  const history = await (await context.request.get(base + '/api/v1/jobs')).json();
  const payload = '<img src=x onerror="window.pwned=true">\n中文日志';
  await writeFile(join(directory, history.data[0].id + '.log'), payload);
  await row.click();
  await expect(page.getByRole('textbox', { name: '任务输出日志' })).toHaveValue(payload);
  expect(await page.evaluate(() => 'pwned' in window)).toBe(false);

  // Deliberately split a UTF-8 character across the API's 64 KiB boundary.
  await writeFile(join(directory, history.data[0].id + '.log'), 'x'.repeat(65535) + '中🙂\n尾部');
  await row.click();
  const output = page.getByRole('textbox', { name: '任务输出日志' });
  await expect.poll(async () => (await output.inputValue()).endsWith('中🙂\n尾部')).toBe(true);
  expect((await output.inputValue()).includes('\uFFFD')).toBe(false);
  await writeFile(join(directory, history.data[0].id + '.log'), 'x'.repeat(240000) + 'bounded-tail');
  await row.click();
  await expect.poll(async () => (await output.inputValue()).endsWith('bounded-tail')).toBe(true);
  expect((await output.inputValue()).length).toBe(200000);
  await expect(page.getByText('页面仅显示最后 200,000 个字符', { exact: false })).toBeVisible();

  await page.getByRole('button', { name: '退出登录' }).click();
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  const replay = await context.request.get(base + '/api/v1/jobs', { headers: { Cookie: `${cookieName}=${session!.value}` } });
  expect(replay.status()).toBe(401);
  await page.reload();
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  expect(errors).toEqual([]);
});
