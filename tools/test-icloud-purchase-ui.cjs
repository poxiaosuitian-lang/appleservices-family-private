// Entirely offline: all page/API requests are fulfilled by fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');
const GiB = 1024 ** 3;
const quota = paid => ({total_bytes:(paid || 5)*GiB,paid_bytes:paid*GiB,used_bytes:0,available_bytes:(paid || 5)*GiB});
async function scenario(browser, {paid=0, discovered=paid, prepareOK=true}={}, check) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const calls=[], errors=[];
  const view={selected:'fixture',account:'Fixture',accounts:[],status:{running:true,authenticated:true,state:'online',pid:123},control_ready:true,icloud_activated:true,store_session_ready:true,purchase_preparation_available:true,gift_card_available:true,storage:quota(paid),storage_stale:false,storage_checked_at:'2026-01-01T00:00:00Z',storage_plan_stale:false,storage_plan_checked_at:'2026-01-01T00:00:00Z',storage_plan:{plans:[{bytes:50*GiB,currency:'USD',price_minor:99,period:'month',available:true}]}};
  page.on('pageerror', e=>errors.push(e.message));
  await page.route('**/*', async route=>{
    const url=new URL(route.request().url());
    if(url.hostname!=='dashboard.test')return route.abort();
    let result;
    if(url.pathname==='/api/view')result=view;
    else if(url.pathname==='/api/action'){
      const action=route.request().postDataJSON().action;calls.push(action);
      if(action==='gift-prepare')result={ok:true,message:'兑换会话已核验',gift_card:{ready:true,state:'prepared',preparation_id:'fixture-preparation'}};
      else if(action==='gift-redeem'){
        const submitted=route.request().postDataJSON();
        assert.equal(submitted.request_id,'fixture-preparation');assert.equal(submitted.code,'X000000000000000');
        assert.equal(await page.locator('#giftCode').inputValue(),'');
        result={ok:true,message:'Apple 已确认入账',gift_card:{state:'completed',credited:'$5.00',balance:'$5.00'}};
      }
      else if(action==='storage-info'){view.storage=quota(discovered);view.storage_checked_at=new Date().toISOString();result={ok:true,storage:view.storage};}
      else if(action==='storage-purchase-prepare-us')result={ok:prepareOK,stage:prepareOK?'purchase-prepared':'purchase-store-authentication',storage:view.storage,storage_plan:view.storage_plan};
      else if(action==='storage-skill-purchase-us'){view.storage=quota(50);view.storage_checked_at=new Date().toISOString();result={ok:true,stage:'purchase-completed',storage:view.storage,purchase:{state:'completed',quota_verified:true}};}
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
    await check(page,calls);
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
    console.log('PASS: payment recheck, existing subscription, failed preparation, cancel and verified purchase (offline)');
  }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
