const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
(async () => {
  const browser = await chromium.launch({headless:true, channel:process.env.BROWSER_CHANNEL || 'msedge'});
  try {
    const page = await browser.newPage(), errors = [], actions = [];
    let profiles = [], selected = '', nextID = 1, loginCalls = 0, loginMode = 'manual', prepareOK = false, defaultKeySaved = false;
    const view = {selected:'fixture',account:'Fixture',login_account:'owner@example.test',accounts:[],status:{running:true,authenticated:true,state:'online',pid:1},control_ready:true,icloud_activated:true,app_password:{state:'ready'},storage:{paid_bytes:0},storage_stale:false};
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.hostname !== 'fixture.test') return route.abort();
      if (url.pathname === '/api/sync-recovery') {
        const input = request.postDataJSON();
        assert.equal(input.profile_id,'1');
        assert.equal(url.searchParams.get('account'),'fixture');
        const profile = profiles.find(item => item.id === input.profile_id);
        assert.equal(input.url,profile.url);
        profile.recovery_enabled = input.enabled;
        return route.fulfill({json:{ok:true,enabled:input.enabled}});
      }
      if (url.pathname === '/api/view') return route.fulfill({json:view});
      if (url.pathname === '/api/sync-profiles') {
        if (request.method() === 'POST') {
          const input = request.postDataJSON();
          if (input.operation === 'save') {
            let profile = profiles.find(p => p.id === input.id);
            if (!profile) { assert(input.token.length >= 6 || (defaultKeySaved && input.token === '')); profile = {id:String(nextID++),key_saved:true}; profiles.push(profile); }
            profile.url = input.url; selected = profile.id;
          } else if (input.operation === 'select') selected = input.id;
          else if (input.operation === 'delete') { profiles = profiles.filter(p => p.id !== input.id); selected = profiles[0]?.id || ''; }
        }
        return route.fulfill({json:{selected,profiles,default_key_saved:defaultKeySaved}});
      }
      if (url.pathname === '/api/action') {
        const input = request.postDataJSON();
        if (input.action === 'app-password-ensure') {
          assert.equal(url.searchParams.get('account'),'fixture');
          actions.push(input);
          view.app_password = {state:prepareOK ? 'ready' : 'verification_required'};
          return route.fulfill({json:{ok:prepareOK,app_password:view.app_password,message:prepareOK ? '已加密保存' : '请完成账户验证'}});
        }
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
    await page.waitForFunction(() => current?.selected === 'fixture');
    assert(await page.locator('#serverSyncButton').isEnabled());
    assert(await page.locator('#hmeCard').isHidden());
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>!syncConfigBusy);
    await page.evaluate(()=>scheduleAutoRefresh({storage:{total_bytes:1},storage_stale:true,storage_checked_at:'fixture-stale'},true));
    assert.equal(await page.evaluate(()=>autoRefreshScheduled),false);
    await page.locator('#serverSyncURL').fill('https://one.example.test');
    await page.locator('#serverSyncToken').fill('k'.repeat(6));
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
    await page.locator('#serverRecoveryEnabled').uncheck();
    await page.waitForFunction(()=>document.getElementById('serverRecoveryStatus').textContent==='已关闭');
    assert.equal(profiles[0].recovery_enabled,false);
    await page.locator('#serverRecoveryEnabled').check();
    await page.waitForFunction(()=>document.getElementById('serverRecoveryStatus').textContent.includes('同步完成后启用'));
    assert.equal(profiles[0].recovery_enabled,false); // Never register a source before importing it.
    await page.evaluate(()=>{busy=true;$('serverSyncForm').dispatchEvent(new Event('submit',{cancelable:true}));busy=false;});
    assert((await page.locator('#serverSyncFeedback').textContent()).includes('正在处理其他操作'));
    assert.equal(actions.length,0);
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
    assert.equal(await page.evaluate(()=>JSON.stringify(localStorage).includes('k'.repeat(6))),false);
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
    await page.locator('#serverSyncClose').click();
    view.app_password = {state:'not_subscribed'};
    view.family = {member:true,storage_participating:true,storage_bytes:50*1024**3};
    await page.reload();
    assert(await page.locator('#serverSyncButton').isEnabled());
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncSubmit').disabled);
    const familyStart = actions.length;
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncFeedback').textContent.includes('请完成账户验证'));
    assert.deepEqual(actions.slice(familyStart).map(a=>a.action),['app-password-ensure']);
    assert(await page.locator('#serverSyncDialog').isVisible());
    prepareOK = true;
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>document.getElementById('serverSyncFeedback').textContent.includes('尚未更新'));
    assert.deepEqual(actions.slice(familyStart).map(a=>a.action),['app-password-ensure','app-password-ensure','hme-server-sync','hme-server-login']);
    await page.locator('#serverSyncSubmit').click();
    await page.waitForFunction(()=>!document.getElementById('serverSyncSubmit').disabled);
    assert.equal(actions.at(-1).action,'hme-server-login');
    assert.equal(actions.length,familyStart+5); // No repeated password creation or import.
    await page.locator('#serverSyncClose').click();
    defaultKeySaved = true;
    await page.reload();
    await page.locator('#serverSyncButton').click();
    await page.waitForFunction(()=>!syncConfigBusy);
    await page.locator('#serverSyncProfile').selectOption('');
    assert.equal(await page.locator('#serverSyncToken').inputValue(),'');
    assert.equal(await page.locator('#serverSyncToken').getAttribute('required'),null);
    assert((await page.locator('#serverSyncToken').getAttribute('placeholder')).includes('默认密钥'));
    await page.locator('#serverSyncURL').fill('https://default.example.test');
    await page.locator('#serverSyncSave').click();
    await page.waitForFunction(()=>!syncConfigBusy);
    assert.equal(profiles.at(-1).url,'https://default.example.test');
    assert.deepEqual(errors,[]);
    console.log('PASS: unsubscribed account sync, save, reload, switch, key masking, sync by profile ID, changed-host validation deletion, automatic server login and manual OTP continuation');
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1});
