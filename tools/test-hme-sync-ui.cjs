const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
(async () => {
  const browser = await chromium.launch({headless:true, channel:process.env.BROWSER_CHANNEL || 'msedge'});
  try {
    const page = await browser.newPage(), errors = [], actions = [];
    let profiles = [], selected = '', nextID = 1, loginCalls = 0, loginMode = 'manual';
    const view = {selected:'fixture',account:'Fixture',login_account:'owner@example.test',accounts:[],status:{running:true,authenticated:true,state:'online',pid:1},control_ready:true,icloud_activated:true,app_password:{state:'ready'},storage:{paid_bytes:53687091200},storage_stale:false};
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.hostname !== 'fixture.test') return route.abort();
      if (url.pathname === '/api/view') return route.fulfill({json:view});
      if (url.pathname === '/api/sync-profiles') {
        if (request.method() === 'POST') {
          const input = request.postDataJSON();
          if (input.operation === 'save') {
            let profile = profiles.find(p => p.id === input.id);
            if (!profile) { assert(input.token.length >= 32); profile = {id:String(nextID++),key_saved:true}; profiles.push(profile); }
            profile.url = input.url; selected = profile.id;
          } else if (input.operation === 'select') selected = input.id;
          else if (input.operation === 'delete') { profiles = profiles.filter(p => p.id !== input.id); selected = profiles[0]?.id || ''; }
        }
        return route.fulfill({json:{selected,profiles}});
      }
      if (url.pathname === '/api/action') {
        const input = request.postDataJSON();
        assert(['hme-server-sync','hme-server-login','hme-server-login-device'].includes(input.action)); assert.equal(input.profile_id,'1'); assert.equal(input.note,'https://one.example.test');
        assert(!input.code); assert.equal(url.searchParams.get('account'),'fixture');
        assert.equal(await page.locator('#serverSyncToken').inputValue(),''); actions.push(input);
        if (input.action.startsWith('hme-server-login')) {
          loginCalls++;
          if (loginMode === 'unavailable') return route.fulfill({json:{ok:false,message:'服务器尚未更新自动登录接口'}});
          if (loginCalls === 1) return route.fulfill({json:{ok:false,message:'请输入受信任设备上的六位验证码',server_login:{state:'otp_required',challenge:'a'.repeat(64),verification:'sms',device_code_supported:true}}});
          assert.equal(input.action,loginMode === 'sms' ? 'hme-server-login' : 'hme-server-login-device');
          assert.equal(input.request_id,'a'.repeat(64)); assert.equal(input.label,'123456');
          assert.equal(await page.locator('#serverSyncOTP').inputValue(),'');
          return route.fulfill({json:{ok:true,message:'Web、IMAP 和 Apple 新通道均已就绪',server_login:{state:'ready'}}});
        }
        return route.fulfill({json:{ok:true,message:'服务器已导入账号'}});
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html','app.js','app.css'].includes(file)) return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(__dirname,'../internal/dashboard/web',file),'utf8')});
    });
    await page.goto('http://fixture.test/#icloud');
    await page.locator('#serverSyncButton').click();
    await page.locator('#serverSyncURL').fill('https://one.example.test');
    await page.locator('#serverSyncToken').fill('k'.repeat(32));
    await page.locator('#serverSyncSave').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncFeedback').textContent.includes('已加密保存'));
    assert.equal(await page.locator('#serverSyncToken').inputValue(),'');
    await page.locator('#serverSyncProfile').selectOption('');
    await page.locator('#serverSyncURL').fill('https://two.example.test');
    await page.locator('#serverSyncToken').fill('z'.repeat(32));
    await page.locator('#serverSyncSave').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncProfile').value==='2');
    await page.locator('#serverSyncClose').click();
    await page.reload();
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncProfile').value==='2');
    assert.equal(await page.locator('#serverSyncURL').inputValue(),'https://two.example.test');
    await page.locator('#serverSyncProfile').selectOption('1');
    assert.equal(await page.locator('#serverSyncURL').inputValue(),'https://one.example.test');
    await page.locator('#serverSyncForm button[type=submit]').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncOTPRow').hidden);
    assert.equal(actions.length,2);
    assert(await page.locator('#serverSyncDialog').isVisible());
    assert.equal(await page.locator('#serverSyncOTPSource').inputValue(),'trusted_device');
    assert((await page.locator('#serverSyncOTP').getAttribute('placeholder')).includes('iPhone'));
    await page.locator('#serverSyncOTP').fill('123456');
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>document.getElementById('notice').textContent.includes('均已就绪'));
    assert.equal(actions.length,3); assert.equal(loginCalls,2);
    assert.equal(await page.evaluate(()=>JSON.stringify(localStorage).includes('k'.repeat(32))),false);
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncProfile').value==='1');
    await page.locator('#serverSyncURL').fill('https://changed.example.test');
    await page.locator('#serverSyncSave').click();
    assert.equal(profiles[0].url,'https://one.example.test');
    page.on('dialog',dialog=>dialog.accept());
    await page.locator('#serverSyncProfile').selectOption('2');
    await page.locator('#serverSyncDelete').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncFeedback').textContent.includes('已删除'));
    assert.equal(profiles.length,1);
    loginMode = 'sms'; loginCalls = 0;
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncOTPRow').hidden);
    await page.locator('#serverSyncOTP').fill('123456');
    await page.locator('#serverSyncOTPSource').selectOption('sms');
    assert.equal(await page.locator('#serverSyncOTP').inputValue(),'');
    assert((await page.locator('#serverSyncOTP').getAttribute('placeholder')).includes('短信'));
    await page.locator('#serverSyncOTP').fill('123456');
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncDialog').open);
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncSubmit').disabled);
    loginMode = 'unavailable';
    const before = actions.length;
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncFeedback').textContent.includes('尚未更新'));
    assert(await page.locator('#serverSyncDialog').isVisible());
    assert.equal(actions.length,before+2);
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncSubmit').disabled);
    assert.equal(actions.length,before+3); // Retry authentication only, not the completed import.
    assert.deepEqual(errors,[]);
    console.log('PASS: save, reload, switch, key masking, sync by profile ID, changed-host validation deletion, automatic server login and manual OTP continuation');
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1});
