// Offline regression fixtures; no Apple requests or family mutations.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const web = path.join(__dirname, '../internal/dashboard/web');
(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.BROWSER_CHANNEL ? {channel:process.env.BROWSER_CHANNEL} : {})});
  try {
    const page = await browser.newPage();
    const errors = [], actions = [];
    page.on('pageerror', e => errors.push(e.message));
    const stamp = new Date().toISOString();
    const detached = {member:false, organizer:false, eligible:true, eligibility_known:true, members:0};
    const organizer = {member:true, organizer:true, members:2, organizer_account:'mother', organizer_name:'Mother'};
    const view = {selected:'mother', accounts:[
      {id:'mother', name:'Mother', running:true, authenticated:true, icloud_activated:true, family_organizer:false},
      {id:'child', name:'Child', running:true, authenticated:true, icloud_activated:true}
    ], family:detached, family_checked_at:stamp, family_stale:false,
    status:{running:true,authenticated:true,state:'online'},control_ready:true,icloud_activated:true};
    await page.addInitScript(() => {
      localStorage.setItem('family-organizer-account', 'mother');
      localStorage.setItem('family-organizer-name', 'Mother');
    });
    await page.route('**/*', route => {
      const u = new URL(route.request().url());
      if (u.hostname !== 'dashboard.test') return route.abort();
      if (u.pathname === '/api/view') return route.fulfill({json:view});
      if (u.pathname === '/api/action') { actions.push(route.request().postDataJSON()); return route.fulfill({json:{ok:false}}); }
      const file = u.pathname === '/' ? 'index.html' : u.pathname.slice(1);
      if (!['index.html','app.js','app.css'].includes(file)) return route.abort();
      return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html',body:fs.readFileSync(path.join(web,file),'utf8')});
    });
    await page.goto('http://dashboard.test/#overview');
    await page.waitForFunction(() => current?.selected === 'mother');
    assert.equal(await page.locator('#accountList .account-role').count(), 0);
    assert.equal(await page.locator('#familyRole').textContent(), '未加入');
    assert.equal(await page.evaluate(() => localStorage.getItem('family-organizer-account')), null);
    const update = () => page.evaluate(v => render(v), view);
    // Same timestamp, different status must update both panels and actions.
    view.family = organizer; view.accounts[0].family_organizer = true;
    await update();
    assert.equal(await page.locator('#accountList .account-role').textContent(), '母号');
    assert.equal(await page.locator('#familyRole').textContent(), '组织者');
    assert.equal(await page.locator('#familyControls').evaluate(e => e.hidden), true);
    assert.equal(await page.locator('#familyRemoveControls').evaluate(e => e.hidden), false);
    view.family_stale = true; view.accounts[0].family_stale = true;
    await update();
    assert.equal(await page.locator('#accountList .account-role').textContent(), '母号');
    assert.equal(await page.locator('#familyRemoveButton').isDisabled(), true);
    await page.waitForTimeout(150);
    assert.deepEqual(actions, []); // Expired cache never triggers an Apple read.
    assert.match(await page.locator('#familyCacheNote').textContent(), /上次刷新/);
    assert(await page.locator('#familyRefreshButton').isEnabled());
    await page.locator('#familyRefreshButton').click();
    await page.waitForFunction(() => !busy);
    assert.deepEqual(actions.map(a => a.action), ['family-info']);
    assert.equal(await page.locator('#familyRole').textContent(), '组织者');
    actions.length = 0;
    view.family = detached; view.family_stale = false; view.accounts[0].family_organizer = false;
    await update();
    assert.equal(await page.locator('#accountList .account-role').count(), 0);
    assert.equal(await page.locator('#familyRole').textContent(), '未加入');
    assert.equal(await page.locator('#familyRemoveControls').evaluate(e => e.hidden), true);
    // Missing/deleted organizers must never be resurrected from the selection cache.
    await page.evaluate(() => {poolOrganizerAccount='removed';poolOrganizerName='Old';});
    view.family = null; view.selected = 'child';
    await update();
    assert.equal(await page.locator('#accountList .account-role').count(), 0);
    assert.equal(await page.locator('#familyStatusChip').textContent(), '待读取');
    // Personal iCloud+ subscribers can host a family. Shared capacity and
    // activation alone must not turn ordinary accounts or members into hosts.
    view.accounts[0].icloud_plus = true;
    view.accounts[1].family_member = true;
    view.accounts[1].icloud_plus = true;
    view.accounts.push({id:'ordinary', name:'Ordinary', icloud_activated:true});
    await update();
    assert.deepEqual(await page.locator('#accountList .account-role').allTextContents(), ['母号','子号']);
    assert.deepEqual(await page.locator('#familyOrganizer option').evaluateAll(es => es.map(e => e.value)), ['', 'mother']);
    view.accounts[0].icloud_plus = false;
    await update();
    assert.deepEqual(await page.locator('#familyOrganizer option').evaluateAll(es => es.map(e => e.value)), ['']);
    assert.deepEqual(await page.locator('#accountList .account-role').allTextContents(), ['子号']);
    assert.deepEqual(actions, []);
    assert.deepEqual(errors, []);
    console.log('family status UI regressions passed');
  } finally { await browser.close(); }
})().catch(e => {console.error(e);process.exitCode=1;});
