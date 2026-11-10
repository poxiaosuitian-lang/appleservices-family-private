// Offline fixtures only. No Apple authentication or mailbox mutation.
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
    page.on('pageerror', e => errors.push(e.message));
    const view = {selected:'fixture',account:'Fixture',accounts:[],status:{running:true,authenticated:true,state:'online'},control_ready:true,icloud_activated:true};
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if(url.hostname !== 'dashboard.test') return route.abort();
      if(url.pathname === '/api/view') return route.fulfill({json:view});
      if(url.pathname === '/api/action') {
        const action = route.request().postDataJSON().action;
        calls.push(action);
        assert(['mail-check','mail-ensure','security-save','security-clear','security-resume'].includes(action));
        if (action === 'security-resume') {
          view.security_questions.blocked = false;
          return route.fulfill({json:{ok:true,message:'继续检查'}});
        }
        if (action === 'security-save') {
          const body = route.request().postDataJSON();
          assert.deepEqual(body.security_answers,[{id:136,answer:'synthetic-private-answer'}]);
          assert.equal(await page.locator('#securityQuestionsFields input').inputValue(),'');
          view.security_questions.questions[0].saved = true;
          return route.fulfill({json:{ok:true,message:'已加密保存'}});
        }
        if (action === 'security-clear') {
          view.security_questions.questions[0].saved = false;
          return route.fulfill({json:{ok:true,message:'已清除'}});
        }
        view.primary_mail = {phase:'ready',email:'fixture@icloud.com',message:'iCloud 主邮箱已存在，无需重复创建',auto_create:true};
        return route.fulfill({json:{ok:true,message:view.primary_mail.message}});
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if(!['index.html','app.js','app.css'].includes(file)) return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(web,file),'utf8')});
    });
    async function refresh() {
      await page.goto('about:blank');
      await page.goto('http://dashboard.test/#icloud');
      await page.waitForFunction(() => !!current?.control_ready);
    }
    await refresh();
    assert(await page.locator('#primaryMailEnsure').isVisible());
    assert.deepEqual(calls,[]); // old accounts are never automatically created by page load
    await page.locator('#primaryMailCheck').click();
    await page.waitForFunction(() => document.getElementById('primaryMailAddress').textContent === 'fixture@icloud.com');
    assert(await page.locator('#primaryMailEnsure').isHidden());
    assert.deepEqual(calls,['mail-check']);
    view.primary_mail = {phase:'unconfirmed',submitted:true,auto_create:true,message:'创建结果尚未确认'};
    await refresh();
    assert(await page.locator('#primaryMailEnsure').isHidden());
    assert(await page.locator('#primaryMailCheck').isEnabled());
    assert.equal(await page.locator('#primaryMailAddress').textContent(),'');
    view.primary_mail = {phase:'checking',auto_create:true,message:'正在检查',updated_at:new Date().toISOString()};
    await refresh();
    assert(await page.locator('#primaryMailCheck').isDisabled());
    assert(await page.locator('#primaryMailEnsure').isDisabled());
    view.primary_mail.updated_at = '2020-01-01T00:00:00Z';
    await refresh();
    assert(await page.locator('#primaryMailCheck').isEnabled());
    view.primary_mail = {phase:'blocked',auto_create:true,message:'需要验证'};
    await refresh();
    assert.equal(await page.locator('#primaryMailEnsure').innerText(),'继续检查并创建');
    await page.locator('#primaryMailEnsure').click();
    await page.waitForFunction(() => document.getElementById('primaryMailEnsure').hidden);
    assert.deepEqual(calls,['mail-check','mail-ensure']);
    view.security_questions = {questions:[{id:136,question:'Fixture question?',saved:false}],blocked:false};
    view.primary_mail.auto_create = true;
    await refresh();
    await page.locator('#securityQuestionsFields input').fill('synthetic-private-answer');
    await page.locator('#securityQuestionsSave').click();
    await page.waitForFunction(() => !busy && document.getElementById('securityQuestionsFields').textContent.includes('已加密保存'));
    assert.deepEqual(calls,['mail-check','mail-ensure','security-save','mail-ensure']);
    assert.equal(await page.locator('#securityQuestionsFields input').inputValue(),'');
    assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('synthetic-private-answer')),false);
    await page.locator('#securityQuestionsClear').click();
    await page.waitForFunction(() => document.getElementById('securityQuestionsClear').disabled);
    assert.equal(calls.at(-1),'security-clear');
    view.security_questions.blocked = true;
    view.security_questions.blocked_reason = 'additional-verification';
    await refresh();
    assert.match(await page.locator('#securityQuestionsSummary').innerText(),/后续安全验证/);
    await page.locator('#securityQuestionsRead').click();
    await page.waitForFunction(() => !busy && !current.security_questions.blocked);
    assert.deepEqual(calls.slice(-2),['security-resume','mail-check']);
    await page.locator('#securityQuestionsFields input').fill('draft-private-answer');
    await page.evaluate(() => { selectedAccount = 'another-fixture'; render({...current, security_questions:null}); });
    assert.equal(await page.locator('#securityQuestionsFields input').count(),0);
    await page.setViewportSize({width:390,height:844});
    assert(await page.locator('#primaryMailSummary').isVisible());
    assert.deepEqual(errors,[]);
    console.log('PASS: mailbox states and encrypted-answer UI; save/continue/clear; no answer retention or cross-account drafts (offline)');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
