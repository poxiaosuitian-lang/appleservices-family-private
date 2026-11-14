// Entirely offline. All accounts and passwords are synthetic fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');
const GiB = 1024 ** 3;
const password = 'abcd-efgh-ijkl-mnop';
(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.BROWSER_CHANNEL ? {channel:process.env.BROWSER_CHANNEL} : {})});
  try {
    const page = await browser.newPage({viewport:{width:1280,height:900}});
    const calls = [], errors = [];
    const view = {selected:'fixture',account:'Fixture',accounts:[],status:{running:true,authenticated:true,state:'online',pid:123},control_ready:true,icloud_activated:true,storage:{total_bytes:55*GiB,paid_bytes:50*GiB,used_bytes:0,available_bytes:55*GiB},storage_checked_at:'2026-01-01T00:00:00Z',storage_stale:false};
    page.on('pageerror', e => errors.push(e.message));
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (url.hostname !== 'dashboard.test') return route.abort();
      if (url.pathname === '/api/view') return route.fulfill({json:view});
      if (url.pathname === '/api/action') {
        const body = route.request().postDataJSON();
        assert.equal(url.searchParams.get('account'),'fixture');
        calls.push(body.action);
        if (body.action === 'app-password-ensure') {
          view.app_password = {state:'verification_required',message:'Apple 要求后续安全验证，请前往 Apple 账户生成。'};
          return route.fulfill({json:{ok:false,message:view.app_password.message,app_password:view.app_password}});
        }
        assert.equal(body.action,'app-password-save');
        assert.equal(body.code,password);
        assert.equal(await page.locator('#appPasswordInput').inputValue(),'');
        view.app_password = {state:'ready',message:'已加密保存',label:'appleservices',updated_at:new Date().toISOString()};
        return route.fulfill({json:{ok:true,message:'已加密保存',app_password:view.app_password}});
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html','app.js','app.css'].includes(file)) return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(web,file),'utf8')});
    });
    const refresh = async () => {
      await page.goto('about:blank');
      await page.goto('http://dashboard.test/#icloud');
      await page.waitForFunction(() => !!current?.selected);
    };
    await refresh();
    assert(await page.locator('#appPasswordEnsure').isEnabled());
    assert(await page.locator('#appPasswordForm').isHidden());
    await page.evaluate(() => poll());
    assert.deepEqual(calls,[]); // polling and missing status never generate passwords

    view.storage.paid_bytes = 0;
    view.app_password = {state:'not_subscribed'};
    await refresh();
    assert(await page.locator('#appPasswordEnsure').isDisabled());
    view.storage.paid_bytes = 50*GiB;
    view.app_password = {state:'pending'};
    await refresh();
    assert(await page.locator('#appPasswordEnsure').isDisabled());
    assert(await page.locator('#appPasswordForm').isHidden());
    delete view.app_password;
    view.control_ready = false;
    await refresh();
    assert(await page.locator('#appPasswordEnsure').isDisabled());
    view.control_ready = true;
    await refresh();
    await page.locator('#appPasswordEnsure').click();
    await page.waitForFunction(() => !busy && !document.getElementById('appPasswordForm').hidden);
    assert.deepEqual(calls,['app-password-ensure']);
    assert.equal(await page.locator('#appPasswordMessage').innerText(),view.app_password.message);
    assert.equal(await page.locator('#appPasswordInput').getAttribute('type'),'password');
    assert(await page.locator('#appPasswordSave').isDisabled());
    await page.locator('#appPasswordInput').fill(password);
    await page.locator('#appPasswordSave').click();
    await page.waitForFunction(() => !busy && document.getElementById('appPasswordSummary').textContent === '应用专用密码：已加密保存');
    assert.deepEqual(calls,['app-password-ensure','app-password-save']);
    assert.equal(await page.locator('#appPasswordInput').inputValue(),'');
    assert(await page.locator('#appPasswordEnsure').isHidden());
    assert(await page.locator('#appPasswordForm').isHidden());
    assert.equal(await page.evaluate(value => JSON.stringify(localStorage).includes(value) || JSON.stringify(sessionStorage).includes(value) || document.body.textContent.includes(value),password),false);
    view.status = {running:false,authenticated:false,state:'stopped'};
    view.control_ready = false;
    await refresh();
    assert.equal(await page.locator('#appPasswordSummary').innerText(),'应用专用密码：已加密保存');
    assert.deepEqual(calls,['app-password-ensure','app-password-save']);

    view.status = {running:true,authenticated:true,state:'online',pid:124};
    view.control_ready = true;
    for (const state of ['failed','unknown']) {
      view.app_password = {state,message:'保留服务器的失败原因：' + state};
      await refresh();
      assert.equal(await page.locator('#appPasswordMessage').innerText(),view.app_password.message);
      assert(await page.locator('#appPasswordForm').isVisible());
    }
    await page.locator('#appPasswordInput').fill(password);
    await page.evaluate(() => render({...current, selected:'second-fixture', app_password:null}));
    assert.equal(await page.locator('#appPasswordInput').inputValue(),'');
    assert(await page.locator('#appPasswordForm').isHidden());
    assert.equal(await page.evaluate(value => JSON.stringify(localStorage).includes(value) || JSON.stringify(sessionStorage).includes(value),password),false);
    await refresh();
    await page.locator('#appPasswordInput').fill(password);
    await page.evaluate(() => render({...current,status:{...current.status,pid:125}}));
    assert.equal(await page.locator('#appPasswordInput').inputValue(),'');
    await page.setViewportSize({width:390,height:844});
    assert(await page.locator('#appPasswordForm').isVisible());
    assert(await page.locator('#appPasswordSave').isVisible());
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth),false);
    assert.deepEqual(errors,[]);
    console.log('PASS: app password states, explicit per-account actions, preserved errors, masked input cleared before save and on account/session changes, no browser retention (offline)');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
