// Offline fixtures: delayed requests exercise login without contacting Apple.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');

(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.BROWSER_CHANNEL ? { channel: process.env.BROWSER_CHANNEL } : {}) });
  try {
    const page = await browser.newPage();
    const errors = [], mutations = [], views = [];
    const accounts = Array.from({ length: 40 }, (_, i) => ({ id: `fixture-${i}`, name: `Fixture ${i}`, running: true, authenticated: true }));
    const devices = Array.from({ length: 20 }, (_, i) => ({ name: `Device ${i}`, model: 'iPhone' }));
    const snapshot = selected => ({ selected, accounts, devices, login_account: 'fixture@example.test', status: { running: true, authenticated: true, state: 'online', pid: 1 }, control_ready: true });
    let holdView = false, delayedView, delayedLogin;
    page.on('pageerror', error => errors.push(error.message));
    // Keep polling deterministic; individual refreshes are exercised below.
    await page.addInitScript(() => {
      const interval = window.setInterval;
      window.setInterval = (callback, delay, ...args) => delay === 2500 ? 0 : interval(callback, delay, ...args);
    });
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (url.hostname !== 'dashboard.test') return route.abort();
      if (url.pathname === '/api/view') {
        const selected = url.searchParams.get('account') || 'fixture-0';
        views.push(selected);
        if (holdView) {
          holdView = false;
          delayedView = () => route.fulfill({ json: snapshot(selected) });
          return;
        }
        return route.fulfill({ json: snapshot(selected) });
      }
      if (['/api/accounts/add', '/api/start'].includes(url.pathname)) {
        mutations.push({ path: url.pathname, account: url.searchParams.get('account'), body: route.request().postDataJSON() });
        delayedLogin = result => route.fulfill({ json: result });
        return;
      }
      if (url.pathname.startsWith('/api/')) {
        mutations.push({ path: url.pathname });
        return route.fulfill({ json: { ok: false } });
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html', 'app.js', 'app.css'].includes(file)) return route.abort();
      return route.fulfill({ contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html', body: fs.readFileSync(path.join(web, file), 'utf8') });
    });
    await page.goto('http://dashboard.test');
    await page.waitForFunction(() => current?.selected === 'fixture-0' && !pollRequest);

    // Repeated unchanged views preserve nodes and keyboard focus.
    assert.equal(await page.evaluate(() => {
      const account = $('accountList').firstChild, device = $('deviceGrid').firstChild;
      account.focus();
      for (let i = 0; i < 10; i++) render(structuredClone(current));
      return account === $('accountList').firstChild && device === $('deviceGrid').firstChild && document.activeElement === account;
    }), true);
    await page.evaluate(() => {
      const next = structuredClone(current);
      next.accounts[0].name = 'Updated fixture';
      next.devices[0].name = 'Updated device';
      render(next);
    });
    assert.match(await page.locator('#accountList').innerText(), /Updated fixture/);
    assert.match(await page.locator('#deviceGrid').innerText(), /Updated device/);

    // A scheduled cache refresh must yield when the user opens login.
    await page.evaluate(() => {
      scheduleAutoRefresh({ family: { member: true }, family_stale: true, family_checked_at: 'fixture' }, true);
      openLogin('add');
    });
    await page.waitForFunction(() => !autoRefreshScheduled);
    await page.evaluate(() => scheduleAutoRefresh({ family: { member: true }, family_stale: true }, true));
    assert.equal(await page.evaluate(() => autoRefreshScheduled), false);
    assert.deepEqual(mutations, []);

    // A slow old-account read must not delay the new account after submission.
    holdView = true;
    const oldView = page.waitForRequest(request => request.url().includes('/api/view'));
    await page.evaluate(() => { void poll(); });
    await oldView;
    await page.locator('#appleID').fill('new@example.test');
    await page.locator('#password').fill('synthetic-password');
    await page.locator('#loginForm button[type=submit]').click();
    await page.waitForFunction(() => loginSubmitting);
    assert.equal(await page.locator('#loginForm button[type=submit]').innerText(), '正在添加…');
    assert.equal(await page.locator('#loginForm').getAttribute('aria-busy'), 'true');
    assert(await page.locator('#password').isDisabled());
    assert.equal(await page.locator('#password').inputValue(), '');
    await page.evaluate(() => $('loginForm').dispatchEvent(new Event('submit', { cancelable: true })));
    assert.equal(mutations.length, 1);
    assert.equal(mutations[0].body.password, 'synthetic-password');
    const readsBeforeCompletion = views.length;
    accounts.push({ id: 'new-fixture', name: 'New fixture' });
    await delayedLogin({ ok: true, account_id: 'new-fixture', message: '正在连接 Apple，请稍等' });
    await page.waitForFunction(() => current?.selected === 'new-fixture' && !busy && !pollRequest);
    assert.equal(views.length, readsBeforeCompletion + 1);
    assert.equal(views.at(-1), 'new-fixture');
    assert.equal(await page.locator('#loginDialog').evaluate(dialog => dialog.open), false);
    await delayedView();
    assert.equal(await page.evaluate(() => selectedAccount), 'new-fixture');
    assert.doesNotMatch(await page.locator('#lastUpdated').innerText(), /中断/);
    assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('synthetic-password')), false);

    // A failed request unlocks and focuses the password field for retry.
    await page.locator('#addAccountButton').click();
    await page.locator('#appleID').fill('retry@example.test');
    await page.locator('#password').fill('synthetic-retry');
    await page.locator('#loginForm button[type=submit]').click();
    await page.waitForFunction(() => loginSubmitting);
    await delayedLogin({ ok: false, message: 'Fixture login failed' });
    await page.waitForFunction(() => !loginSubmitting && !pollRequest);
    assert.equal(await page.locator('#loginDialog').evaluate(dialog => dialog.open), true);
    assert(await page.locator('#loginForm button[type=submit]').isEnabled());
    assert.equal(await page.locator('#password').inputValue(), '');
    assert.equal(await page.evaluate(() => document.activeElement.id), 'password');
    assert.match(await page.locator('#notice').innerText(), /Fixture login failed/);

    // Reconnect captures its original mode even if the dialog is closed mid-request.
    await page.locator('#closeLogin').click();
    await page.evaluate(() => openLogin('reconnect'));
    await page.locator('#password').fill('synthetic-reconnect');
    await page.locator('#loginForm button[type=submit]').click();
    await page.waitForFunction(() => loginSubmitting);
    await page.locator('#closeLogin').click();
    await page.locator('#addAccountButton').click();
    assert.equal(await page.evaluate(() => loginMode), 'reconnect');
    assert.equal(mutations.at(-1).path, '/api/start');
    assert.equal(mutations.at(-1).account, 'new-fixture');
    await delayedLogin({ ok: true, message: 'Fixture reconnect started' });
    await page.waitForFunction(() => !busy && !pollRequest);

    // Sidebar switching also supersedes a slow read without waiting for the timer.
    holdView = true;
    const pendingView = page.waitForRequest(request => request.url().includes('/api/view'));
    await page.evaluate(() => { void poll(); });
    await pendingView;
    await page.locator('#accountList .account-item').nth(1).click();
    await page.waitForFunction(() => current?.selected === 'fixture-1' && !pollRequest);
    await delayedView();
    assert.equal(await page.evaluate(() => selectedAccount), 'fixture-1');
    assert.deepEqual(errors, []);
    console.log('PASS: immediate login feedback, single refresh, stale-request cancellation, retry, reconnect, cache refresh deferral and stable list nodes (offline)');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
