// All requests use synthetic fixtures; no Apple requests or credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');
(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.BROWSER_CHANNEL ? {channel:process.env.BROWSER_CHANNEL} : {})});
  try {
    const page = await browser.newPage({viewport:{width:1280,height:900}});
    const errors = [], calls = [];
    let codeResponse;
    let view = {selected:'fixture',account:'Fixture',accounts:[],status:{pid:1,running:true,authenticated:false,state:'awaiting_device_code',verification_required:true},control_ready:true,icloud_activated:true,code_ready:false};
    page.on('pageerror', e => errors.push(e.message));
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (url.hostname !== 'dashboard.test') return route.abort();
      if (url.pathname === '/api/view') return route.fulfill({json:view});
      if (url.pathname === '/api/action') {
        const body = route.request().postDataJSON();
        calls.push(body.action);
        if (body.action === 'code') return new Promise(resolve => {
          codeResponse = async () => {
            await route.fulfill({json:{ok:true,code:'123456',hide_at:new Date(Date.now()+60000).toISOString()}});
            resolve();
          };
        });
        assert(['submit-sms','submit-account-code'].includes(body.action));
        assert.equal(body.code,'654321');
        assert.equal(await page.locator('#smsCode').inputValue(),'');
        return route.fulfill({json:{ok:false,message:'Fixture: verification rejected'}});
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html','app.js','app.css'].includes(file)) return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(web,file),'utf8')});
    });
    async function render() { await page.evaluate(v => render(v), view); }
    await page.goto('http://dashboard.test/#overview');
    await page.waitForFunction(() => current?.status?.state === 'awaiting_device_code');
    assert.match(await page.locator('#sessionDescription').innerText(),/受信任 Apple 设备/);
    assert.doesNotMatch(await page.locator('#sessionDescription').innerText(),/手机号/);
    assert.equal(await page.locator('#authState').innerText(),'等待验证');
    assert.equal(await page.locator('#smsCode').getAttribute('placeholder'),'六位设备验证码');
    assert.equal(await page.locator('#smsCode').getAttribute('aria-label'),'六位设备验证码');
    assert(await page.locator('#codeButton').isDisabled());
    assert(await page.locator('#resendButton').isHidden());
    assert.deepEqual(calls,[]); // polling and rendering never generate or submit codes

    view.status.local_verification = 'failed'; await render();
    assert.match(await page.locator('#smsPanel p').innerText(),/本机验证未完成/);
    assert.match(await page.locator('#codeDetail').innerText(),/本机验证未完成/);
    assert.doesNotMatch(await page.locator('#smsPanel p').innerText(),/不是.*受信任|未受信任/);
    view.status.local_verification = 'request_failed'; await render();
    assert.match(await page.locator('#sessionDescription').innerText(),/验证码发送请求未完成/);
    assert.match(await page.locator('#smsPanel p').innerText(),/验证码发送请求未完成/);

    await page.locator('#smsCode').fill('654321');
    view.status.state = 'awaiting_sms'; await render();
    assert.equal(await page.locator('#smsCode').inputValue(),'');
    assert.match(await page.locator('#sessionDescription').innerText(),/手机号/);
    assert.equal(await page.locator('#smsCode').getAttribute('placeholder'),'六位短信验证码');
    assert(await page.locator('#resendButton').isVisible());
    await page.locator('#smsCode').fill('654321');
    await page.locator('#smsForm button[type=submit]').click();
    await page.waitForFunction(() => !busy && document.getElementById('notice').textContent.includes('Fixture'));
    assert.equal(calls.at(-1),'submit-sms');

    view.status.state = 'awaiting_account_validation'; await render();
    assert.match(await page.locator('#smsCode').getAttribute('aria-label'),/设备/);
    await page.locator('#smsCode').fill('654321');
    await page.locator('#smsForm button[type=submit]').click();
    await page.waitForFunction(() => !busy);
    assert.equal(calls.at(-1),'submit-account-code');
    view.status.state = 'awaiting_existing_sms'; await render();
    assert.match(await page.locator('#smsPanel p').innerText(),/本次不会再次发送短信/);
    assert(await page.locator('#resendButton').isHidden());

    // Saved activation must not make a failed current login appear authenticated.
    view.status = {pid:1,running:false,authenticated:false,state:'failed'}; await render();
    assert.equal(await page.locator('#authState').innerText(),'未登录');
    assert.match(await page.locator('#sessionTitle').innerText(),/登录未完成/);
    assert.match(await page.locator('#sessionDescription').innerText(),/本次登录未完成/);

    // Only a fully authenticated worker may expose a code, even if a stale view says ready.
    view.code_ready = true; await render();
    assert(await page.locator('#codeButton').isDisabled());
    view.status = {pid:1,running:true,authenticated:true,state:'online'}; await render();
    assert(await page.locator('#codeButton').isEnabled());
    view.control_ready = false; await render();
    assert(await page.locator('#codeButton').isDisabled());
    view.control_ready = true; await render();
    await page.locator('#codeButton').click();
    assert(codeResponse);
    view = {...view,selected:'other-fixture',account:'Other fixture',status:{...view.status,pid:2}};
    await render();
    await codeResponse();
    await page.waitForFunction(() => !busy);
    assert.equal(await page.evaluate(() => privateCode),'');
    assert(await page.locator('#copyButton').isDisabled());
    assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('123456')),false);
    assert.deepEqual(errors,[]);
    console.log('PASS: device/SMS verification labels, local failure states, credential isolation and no automatic OTP submission (offline)');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
