const { chromium } = require('playwright');
const fs = require('node:fs');
const assert = require('node:assert/strict');
const path = require('node:path');
const output = process.env.JBS_BROWSER_ARTIFACTS || path.join(__dirname, 'artifacts', 'browser');
fs.mkdirSync(output, { recursive: true });
let browser;
(async () => {
  browser = await chromium.launch({ args: ['--no-sandbox'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  await page.addInitScript(() => {
    const NativeSocket = window.WebSocket;
    window.__testSockets = [];
    window.WebSocket = class extends NativeSocket {
      constructor(...args) { super(...args); window.__testSockets.push(this); }
    };
  });
  const errors = [];
  let view;
  const events = new Map();
  page.on('pageerror', e => errors.push(e.message));
  page.on('websocket', ws => ws.on('framereceived', frame => {
    try {
      const m = JSON.parse(frame.payload);
      if (m.t === 'view' && (!view || m.view.state_id >= view.state_id)) view = m.view;
      if (m.t === 'event') events.set(m.event.seq, m.event);
    } catch {}
  }));
  await page.goto(process.env.JBS_BROWSER_URL || 'http://127.0.0.1:5173/');
  await page.getByRole('button', { name: '查看角色 / 创建房间' }).click();
  await page.getByRole('radio', { name: '修经理', exact: true }).check();
  await page.getByRole('textbox', { name: '你的称呼' }).fill('浏览器玩家');
  for (const name of ['韩乘务', '洪船长', '林歌手', '张二副']) await page.getByRole('checkbox', { name, exact: true }).check();
  await page.getByRole('button', { name: '创建并登船' }).click();
  await page.getByText('实时连接', { exact: true }).waitFor();
  await page.screenshot({ path: output + '/lobby.png', fullPage: true });
  const session = await page.evaluate(() => JSON.parse(localStorage.getItem('fog-harbor.session.v1')));
  assert.ok(session.token && session.host_token);
  const started = Date.now();
  const counts = { ready: 0, speech: 0, search: 0, publish: 0, pm: 0, vote: 0 };
  const snapshots = new Set();
  let privateSent = false, published = false, reconnected = false, replayed = 0;
  const changed = (old, type = 'player.ready') => page.waitForFunction(() => true).then(async () => {
    await new Promise((resolve, reject) => {
      const timer = setInterval(() => { if ([...events.values()].some(e => e.seq > old && e.actor === 'xiu' && e.type === type && view?.last_seq >= e.seq)) { clearInterval(timer); clearTimeout(deadline); resolve(); } }, 20);
      const deadline = setTimeout(() => { clearInterval(timer); reject(new Error('State did not change')); }, 10000);
    });
  });
  while (Date.now() - started < 120000) {
    if (!view) { await page.waitForTimeout(50); continue; }
    if (view.ended) break;
    const current = view;
    const allowed = current.allowed_actions || [];
    const has = type => allowed.some(a => a.type === type);
    if (!snapshots.has(current.phase.id) && ['background','search1','vote'].includes(current.phase.id)) {
      await page.screenshot({ path: `${output}/${current.phase.id}.png`, fullPage: true });
      snapshots.add(current.phase.id);
    }
    if (!reconnected && current.phase.id === 'background') {
      await page.reload(); await page.getByText('实时连接', { exact: true }).waitFor();
      await page.getByRole('combobox', { name: '朗读语速' }).selectOption('1.25');
      await page.getByRole('button', { name: '朗读我的剧本' }).click();
      const hasChineseVoice = await page.evaluate(() => speechSynthesis.getVoices().some(voice => /^(zh|cmn|yue)(-|_|$)/i.test(voice.lang)));
      if (!hasChineseVoice) await page.getByText('浏览器未提供中文语音。请在系统中安装中文语音，或改用文字阅读。', { exact: true }).waitFor();
      const cursor = Math.max(0, ...events.keys());
      let release, notify;
      const held = new Promise(resolve => { release = resolve; });
      const blocked = new Promise(resolve => { notify = resolve; });
      const replayRoute = async route => { notify(); await held; await route.continue(); };
      await page.route(`**/api/rooms/${session.room_id}/log**`, replayRoute);
      await page.evaluate(() => window.__testSockets.at(-1).close());
      await Promise.race([blocked, new Promise((_, reject) => setTimeout(() => reject(new Error('Reconnect did not request missed events')), 10000))]);
      const base = new URL(`/api/rooms/${session.room_id}/`, page.url()).href;
      const headers = { Authorization: `Bearer ${session.token}` };
      const fresh = await (await context.request.get(base + 'view', { headers })).json();
      const ready = await context.request.post(base + 'action', { headers, data: { type: 'ready', state_id: fresh.state_id, payload: {} } });
      assert.equal(ready.status(), 200, 'out-of-band ready failed'); counts.ready++;
      let missed = [], speeches = [];
      const deadline = Date.now() + 5000;
      do {
        missed = await (await context.request.get(base + `log?since=${cursor}`, { headers })).json();
        speeches = missed.filter(event => event.type === 'chat.say');
      } while (!speeches.length && Date.now() < deadline);
      assert.ok(speeches.length, 'no speech occurred while disconnected');
      assert.equal(await page.getByText(speeches[0].payload.text, { exact: true }).count(), 0, 'disconnected client received a live speech');
      release();
      await page.getByText('实时连接', { exact: true }).waitFor();
      await page.unroute(`**/api/rooms/${session.room_id}/log**`, replayRoute);
      for (const event of missed) events.set(event.seq, event);
      for (const speech of speeches) {
        const speaker = view.seats.find(seat => seat.id === speech.actor)?.char_name;
        assert.ok(speaker);
        const rendered = page.locator('article.message').filter({ has: page.getByText(speaker, { exact: true }) }).getByText(speech.payload.text, { exact: true });
        await rendered.waitFor();
        assert.equal(await rendered.count(), 1, 'replayed speech duplicated');
      }
      replayed = speeches.length; reconnected = true;
      continue;
    }
    if (has('search')) {
      await page.getByRole('tab', { name: '甲板与线索' }).click();
      const available = page.locator('.location-manifest').getByRole('button').filter({ hasText: /[1-9]\d* 条/ });
      const captain = available.filter({ hasText: '船长室' });
      if (!await available.count()) continue;
      await (await captain.count() ? captain.first() : available.first()).click();
      const investigate = page.getByRole('button', { name: '调查此处' });
      if (!await investigate.isEnabled()) continue;
      const old = Math.max(0, ...events.keys());
      await investigate.click();
      await changed(old, 'clue.found'); counts.search++;
    } else if (has('say')) {
      if (has('pm.send') && !privateSent) {
        await page.getByRole('tab', { name: '私信', exact: true }).click();
        await page.getByRole('textbox', { name: '私信内容' }).fill('浏览器私信隔离测试，请核实时间线。');
        const old = Math.max(0, ...events.keys());
        await page.getByRole('button', { name: '发送私信' }).click(); await changed(old, 'pm.send');
        await page.getByText('浏览器私信隔离测试，请核实时间线。', { exact: true }).waitFor();
        privateSent = true; counts.pm++;
        await page.getByRole('tab', { name: '公开讨论' }).click();
        assert.equal(await page.getByText('浏览器私信隔离测试，请核实时间线。', { exact: true }).count(), 0);
      }
      if (has('publish') && !published && current.data.clues.own.length) {
        await page.getByRole('tab', { name: '甲板与线索' }).click();
        const old = Math.max(0, ...events.keys());
        await page.getByRole('button', { name: '公开这条线索' }).first().click(); await changed(old, 'clue.published');
        published = true; counts.publish++;
      }
      await page.getByRole('tab', { name: '公开讨论' }).click();
      await page.getByRole('textbox', { name: '公开发言内容' }).fill(`第${counts.speech+1}次证词：我是修经理，请大家对照搜证内容核实案发时间线，不要仅凭传闻下结论。`);
      const old = Math.max(0, ...events.keys());
      await page.getByRole('button', { name: '发表证词' }).click(); await changed(old, 'chat.say'); counts.speech++;
    } else if (has('vote.cast')) {
      await page.getByRole('radio', { name: '修经理', exact: true }).check();
      const old = Math.max(0, ...events.keys());
      await page.getByRole('button', { name: '确认指认' }).click(); await changed(old, 'vote.cast'); counts.vote++;
    } else if (has('ready')) {
      const old = Math.max(0, ...events.keys());
      await page.getByRole('button', { name: '我已准备好', exact: true }).first().click();
      await changed(old); counts.ready++;
    } else await page.waitForTimeout(50);
  }
  assert.ok(view?.ended, 'browser game incomplete');
  assert.equal(counts.speech, 5); assert.equal(counts.search, 5); assert.equal(counts.vote, 1);
  assert.equal(counts.pm, 1); assert.equal(counts.publish, 1);
  await page.getByRole('heading', { name: '真相', exact: true }).waitFor();
  await page.screenshot({ path: output + '/end.png', fullPage: true });
  const voices = await page.evaluate(() => speechSynthesis.getVoices().map(v => ({ name: v.name, lang: v.lang })));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: output + '/mobile.png', fullPage: true });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile horizontal overflow');
  assert.deepEqual(errors, []);
  const result = { room: session.room_id, counts, ended: view.ended, reconnected, replayed, voices, errors, seconds: (Date.now()-started)/1000 };
  fs.writeFileSync(output + '/result.json', JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result, null, 2));
  await browser.close();
})().catch(async error => { console.error(error); await browser?.close(); process.exitCode = 1; });
