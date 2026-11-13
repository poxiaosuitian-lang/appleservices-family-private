// Entirely offline: all page/API requests are fulfilled by fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');
const GiB = 1024 ** 3;
const quota = paid => ({total_bytes:(paid || 5)*GiB,paid_bytes:paid*GiB,used_bytes:0,available_bytes:(paid || 5)*GiB});
async function scenario(browser, {paid=0, discovered=paid, prepareOK=true, purchaseExpired=false}={}, check) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const calls=[], errors=[], actionAccounts=[];
  const view={selected:'fixture',account:'Fixture',accounts:[],status:{running:true,authenticated:true,state:'online',pid:123},control_ready:true,icloud_activated:true,store_session_ready:true,purchase_preparation_available:true,gift_card_available:true,storage:quota(paid),storage_stale:false,storage_checked_at:'2026-01-01T00:00:00Z',storage_plan_stale:false,storage_plan_checked_at:'2026-01-01T00:00:00Z',storage_plan:{plans:[{bytes:50*GiB,currency:'USD',price_minor:99,period:'month',available:true}]}};
  const views=new Map([['fixture',view]]);
  page.on('pageerror', e=>errors.push(e.message));
  await page.route('**/*', async route=>{
    const url=new URL(route.request().url());
    if(url.hostname!=='dashboard.test')return route.abort();
    let result;
    if(url.pathname==='/api/view'){
      result=views.get(url.searchParams.get('account')||'fixture');
      assert(result,'unknown account requested');
    }
    else if(url.pathname==='/api/accounts/add'){
      const submitted=route.request().postDataJSON();
      assert.equal(submitted.apple_id,'owner@example.test');
      assert.equal(submitted.password,'synthetic-ui-password');
      assert.equal(await page.locator('#password').inputValue(),'');
      calls.push(submitted.remember_password?'login-remember':'login-once');
      const added={...view,selected:'fixture-new',account:'New fixture',status:{...view.status,pid:124},storage:quota(0)};
      views.set(added.selected,added);
      result={ok:true,account_id:added.selected,message:'fixture login accepted'};
    }
    else if(url.pathname==='/api/action'){
      const action=route.request().postDataJSON().action;calls.push(action);
      const account=url.searchParams.get('account');
      const view=views.get(account);
      assert(view,'action must target a known account');
      actionAccounts.push({action,account});
      if(action==='gift-prepare')result={ok:true,message:'兑换会话已核验',gift_card:{ready:true,state:'prepared',preparation_id:'fixture-preparation'}};
      else if(action==='gift-redeem'){
        const submitted=route.request().postDataJSON();
        assert.equal(submitted.request_id,'fixture-preparation');assert.equal(submitted.code,'X000000000000000');
        assert.equal(await page.locator('#giftCode').inputValue(),'');
        result={ok:true,message:'Apple 已确认入账',gift_card:{state:'completed',credited:'$5.00',balance:'$5.00'}};
      }
      else if(action==='storage-info'){view.storage=quota(discovered);view.storage_checked_at=new Date().toISOString();result={ok:true,storage:view.storage};}
      else if(action==='storage-purchase-prepare-us'){
        view.store_session_ready=prepareOK;
        result={ok:prepareOK,stage:prepareOK?'purchase-prepared':'purchase-store-authentication',storage:view.storage,storage_plan:view.storage_plan,store_authentication:{ready:prepareOK}};
      }
      else if(action==='storage-skill-purchase-us'){
        if(purchaseExpired){
          view.store_session_ready=false;
          result={ok:false,stage:'purchase-store-token-expired',http_status:200,apple_code:'2034',message:'购买会话已过期'};
        }else{
          view.storage=quota(50);view.storage_checked_at=new Date().toISOString();result={ok:true,stage:'purchase-completed',storage:view.storage,purchase:{state:'completed',quota_verified:true}};
        }
      }
      else throw new Error('Unexpected action: '+action);
    }else{
      const file=url.pathname==='/'?'index.html':url.pathname.slice(1);
      if(!['index.html','app.js','app.css'].includes(file))return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(web,file),'utf8')});
    }
    return route.fulfill({json:result});
  });
  try{
    await page.goto('http://dashboard.test/#icloud');
    await page.waitForFunction(()=>!document.getElementById('paymentCheckButton').disabled);
    await check(page,calls,actionAccounts);
    assert.deepEqual(errors,[]);
  }finally{await context.close();}
}
(async()=>{
  const browser=await chromium.launch({headless:true,...(process.env.BROWSER_CHANNEL?{channel:process.env.BROWSER_CHANNEL}:{})});
  try{
    await scenario(browser,{},async(p,c)=>{
      await p.locator('#paymentCheckButton').click();
      await p.waitForFunction(()=>document.getElementById('notice').textContent.includes('预检查通过'));
      assert.deepEqual(c,['storage-info','storage-purchase-prepare-us']);
      p.on('dialog',d=>d.dismiss());await p.locator('#purchaseButton').click();
      await p.waitForFunction(()=>!document.getElementById('paymentCheckButton').disabled);
      assert(!c.includes('storage-skill-purchase-us'));
    });
    await scenario(browser,{paid:50},async(p,c)=>{
      assert(await p.locator('#purchaseButton').isDisabled());
      assert.equal(await p.locator('#hmeCard').evaluate(el=>el.hidden),false,'personal iCloud+ must expose hidden email settings without family membership');
      assert.match(await p.locator('#purchaseAvailability').innerText(),/已开通.*50GB/);
      await p.reload();await p.waitForFunction(()=>!document.getElementById('paymentCheckButton').disabled);
      assert(await p.locator('#purchaseButton').isDisabled());assert.deepEqual(c,[]);
    });
    await scenario(browser,{discovered:50},async(p,c)=>{
      await p.locator('#paymentCheckButton').click();
      await p.waitForFunction(()=>document.getElementById('purchaseButton').disabled&&!document.getElementById('paymentCheckButton').disabled);
      assert.deepEqual(c,['storage-info']);
    });
    await scenario(browser,{prepareOK:false},async(p,c)=>{
      await p.locator('#paymentCheckButton').click();
      await p.waitForFunction(()=>document.getElementById('notice').classList.contains('error'));
      assert.deepEqual(c,['storage-info','storage-purchase-prepare-us']);
    });
    await scenario(browser,{},async(p,c)=>{
      p.on('dialog',d=>d.accept());await p.locator('#purchaseButton').click();
      await p.waitForFunction(()=>document.getElementById('purchaseButton').textContent==='iCloud+ 已开通');
      assert.deepEqual(c,['storage-purchase-prepare-us','storage-skill-purchase-us']);
      assert(await p.locator('#purchaseButton').isDisabled());
    });
    await scenario(browser,{purchaseExpired:true},async(p,c)=>{
      p.on('dialog',d=>d.accept());
      await p.locator('#purchaseButton').click();
      await p.waitForFunction(()=>document.getElementById('purchaseResult').textContent.includes('2034')&&!document.getElementById('paymentCheckButton').disabled);
      assert.deepEqual(c,['storage-purchase-prepare-us','storage-skill-purchase-us']);
      assert.match(await p.locator('#purchaseResult').innerText(),/购买会话已失效.*重新检查付款/);
      assert.match(await p.locator('#storeAuthSummary').innerText(),/不可用.*重新验证/);
      assert.equal(await p.evaluate(()=>storeAuthReady),false);
      await p.locator('#paymentCheckButton').click();
      await p.waitForFunction(()=>document.getElementById('notice').textContent.includes('预检查通过'));
      assert.equal(await p.evaluate(()=>storeAuthReady),true);
      assert.deepEqual(c,['storage-purchase-prepare-us','storage-skill-purchase-us','storage-info','storage-purchase-prepare-us']);
      assert.notEqual(await p.locator('#purchaseButton').innerText(),'iCloud+ 已开通');
    });
    await scenario(browser,{},async(p,c)=>{
      assert(await p.locator('#giftCode').isDisabled());
      await p.locator('#giftPrepareButton').click();
      await p.waitForFunction(()=>!document.getElementById('giftCode').disabled);
      await p.locator('#giftCode').fill('X000000000000000');
      p.once('dialog',d=>d.dismiss());await p.locator('#giftRedeemButton').click();
      assert.deepEqual(c,['gift-prepare']);
      p.once('dialog',d=>d.accept());await p.locator('#giftRedeemButton').click();
      await p.waitForFunction(()=>document.getElementById('giftResult').textContent.includes('余额 $5.00'));
      assert.deepEqual(c,['gift-prepare','gift-redeem']);
      assert(await p.locator('#giftRedeemButton').isDisabled());
      await p.reload();await p.waitForFunction(()=>!document.getElementById('paymentCheckButton').disabled);
      assert.equal(await p.locator('#giftCode').inputValue(),'');assert(await p.locator('#giftCode').isDisabled());
    });
    for(const remember of [false,true]){
      await scenario(browser,{},async(p,c)=>{
        await p.evaluate(()=>openLogin('add'));
        assert.equal(await p.locator('#rememberPassword').isChecked(),false);
        assert((await p.locator('#rememberPassword').boundingBox()).width<32);
        await p.locator('#appleID').fill('owner@example.test');
        await p.locator('#password').fill('synthetic-ui-password');
        await p.locator('#rememberPassword').setChecked(remember);
        await p.locator('#loginForm button[type=submit]').click();
        await p.waitForFunction(()=>!document.getElementById('loginDialog').open);
        assert.deepEqual(c,[remember?'login-remember':'login-once']);
        assert.equal(await p.evaluate(()=>JSON.stringify(localStorage).includes('synthetic-ui-password')),false);
      });
    }
    await scenario(browser,{paid:50},async(p,c,targets)=>{
      // Start with a different account's paid plan and prepared redemption.
      assert(await p.locator('#purchaseButton').isDisabled());
      await p.locator('#giftPrepareButton').click();
      await p.waitForFunction(()=>!document.getElementById('giftCode').disabled);
      await p.evaluate(()=>openLogin('add'));
      await p.locator('#appleID').fill('owner@example.test');
      await p.locator('#password').fill('synthetic-ui-password');
      await p.locator('#loginForm button[type=submit]').click();
      await p.waitForFunction(()=>!document.getElementById('loginDialog').open&&!document.getElementById('purchaseButton').disabled);
      assert.equal(await p.evaluate(()=>localStorage.getItem('selected-account')),'fixture-new');
      assert(await p.locator('#giftCode').isDisabled(),'old account redemption preparation must be cleared');
      await p.locator('#giftPrepareButton').click();
      await p.waitForFunction(()=>!document.getElementById('giftCode').disabled);
      p.on('dialog',d=>d.accept());
      await p.locator('#purchaseButton').click();
      await p.waitForFunction(()=>document.getElementById('purchaseButton').textContent==='iCloud+ 已开通');
      assert.deepEqual(targets,[
        {action:'gift-prepare',account:'fixture'},
        {action:'gift-prepare',account:'fixture-new'},
        {action:'storage-purchase-prepare-us',account:'fixture-new'},
        {action:'storage-skill-purchase-us',account:'fixture-new'},
      ]);
      await p.reload();
      await p.waitForFunction(()=>document.getElementById('purchaseButton').textContent==='iCloud+ 已开通');
      assert.equal(await p.evaluate(()=>localStorage.getItem('selected-account')),'fixture-new');
    });
    console.log('PASS: payment/gift flows, account selection and encrypted-password opt-in (offline)');
  }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
