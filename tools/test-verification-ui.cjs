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
    let view = {selected:'fixture',account:'Fixture',login_account:'fixture@example.test',accounts:[{id:'fixture',name:'Fixture'}],status:{pid:1,running:true,authenticated:false,state:'awaiting_device_code',verification_required:true},control_ready:true,icloud_activated:true,code_ready:false};
    page.on('pageerror', e => errors.push(e.message));
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (url.hostname !== 'dashboard.test') return route.abort();
      if (url.pathname === '/api/view') return route.fulfill({json:view});
      if (url.pathname === '/api/action') {
        const body = route.request().postDataJSON();
        calls.push(body.action);
        if (['code','local-device-code'].includes(body.action)) return new Promise(resolve => {
          const owner = url.searchParams.get('account');
          codeResponse = async () => {
            await route.fulfill({json:{ok:true,code:'123456',account_id:owner,code_kind:body.action==='local-device-code'?'local-device':'',hide_at:new Date(Date.now()+60000).toISOString()}});
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

    // Waiting accounts can reopen their own login without stopping first.
    assert(await page.locator('#reloginButton').isVisible());
    assert(await page.locator('#reloginButton').isEnabled());
    await page.locator('#reloginButton').click();
    assert.equal(await page.evaluate(() => loginMode), 'reconnect');
    assert.equal(await page.evaluate(() => loginAccount), 'fixture');
    assert.equal(await page.locator('#appleID').inputValue(), 'fixture@example.test');
    assert(await page.locator('#appleID').evaluate(input => input.readOnly));
    assert.equal(await page.locator('#password').inputValue(), '');
    assert.deepEqual(calls, []); // Opening the dialog does not stop or log in.
    await page.locator('#closeLogin').click();

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
    assert(await page.locator('#reloginButton').isHidden());
    assert(await page.locator('#startButton').isVisible());

    // Saved account-bound device data can generate a local code while logged out.
    view.local_code_available = true; await render();
    assert(await page.locator('#codeButton').isEnabled());
    assert.equal(await page.locator('#codeButton').innerText(), '显示本机验证码');
    assert.match(await page.locator('#codeDetail').innerText(), /Apple 验证结果/);
    await page.locator('#codeButton').click();
    assert(codeResponse);
    await codeResponse();
    await page.waitForFunction(() => !busy && privateCode === '123456');
    assert.equal(calls.at(-1), 'local-device-code');
    assert.equal(await page.locator('#codeCardTitle').innerText(), '本机验证码');
    assert.match(await page.locator('#codeDetail').innerText(), /本机生成/);
    assert.equal(await page.locator('#authState').innerText(), '未登录');
    await page.evaluate(() => clearCode());
    view.local_code_available = false; await render();

    // Only a fully authenticated worker may expose a code, even if a stale view says ready.
    view.code_ready = true; await render();
    assert(await page.locator('#codeButton').isDisabled());
    view.status = {pid:1,running:true,authenticated:true,state:'online'}; await render();
    view.status.state = 'device_identity_refresh_required';
    Object.assign(view.accounts[0], view.status);
    await render();
    assert.equal(await page.locator('#statusBadge').innerText(), '设备需验证');
    assert.match(await page.locator('#accountList').innerText(), /设备需验证/);
    assert.match(await page.locator('#codeDetail').innerText(), /设备会话需验证/);
    assert(await page.locator('#codeButton').isDisabled());
    view.status.state = view.accounts[0].state = 'online';
    view.status.pairing_state = 'awaiting_client_proof'; await render();
    await page.evaluate(() => showCode({code:'123456',code_kind:'circle'}));
    assert.equal(await page.locator('#codeCardTitle').innerText(),'设备配对码');
    await render();
    assert(await page.locator('#copyButton').isEnabled());
    for (const state of ['exchange_failed','exchange_send_failed']) {
      view.status.pairing_state = state; await render();
      assert.equal(await page.evaluate(() => privateCode),'');
      assert(await page.locator('#copyButton').isDisabled());
      assert(await page.locator('#capabilityNotice.error').isVisible());
      assert.match(await page.locator('#capabilityNotice').innerText(),/iPhone/);
    }
    await page.evaluate(() => showCode({code:'123456'}));
    await render();
    assert.equal(await page.locator('#codeCardTitle').innerText(),'登录验证码');
    assert(await page.locator('#copyButton').isEnabled());
    await page.evaluate(() => clearCode());
    delete view.status.pairing_state; await render();
    assert(await page.locator('#reloginButton').isEnabled());
    await page.evaluate(() => { busy = true; render(current); });
    assert(await page.locator('#reloginButton').isDisabled());
    await page.evaluate(() => { busy = false; render(current); });
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
    // A late local-code response must not appear under a different account.
    view.local_code_available = true;
    view.status = {pid:2,running:false,authenticated:false,state:'failed'};
    await render();
    await page.locator('#codeButton').click();
    view = {...view,selected:'third-fixture',account:'Third fixture'};
    await render();
    await codeResponse();
    await page.waitForFunction(() => !busy);
    assert.equal(await page.evaluate(() => privateCode),'');
    assert(await page.locator('#copyButton').isDisabled());
    assert.deepEqual(errors,[]);
    console.log('PASS: device/SMS verification labels, local failure states, credential isolation and no automatic OTP submission (offline)');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
