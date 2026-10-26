// Drives a real Chromium with the real extension. Args: <profileDir> <port>
const { chromium } = require('playwright');
const [profile, port, urlsJson] = process.argv.slice(2);
const urls = JSON.parse(urlsJson);
(async () => {
  const ext = process.env.EXT_DIR;
  const ctx = await chromium.launchPersistentContext(profile, {
    headless: false,
    args: [
      `--disable-extensions-except=${ext}`, `--load-extension=${ext}`,
      '--host-resolver-rules=MAP * 127.0.0.1',
      '--no-first-run', '--no-default-browser-check',
    ],
  });
  const log = (...a) => console.log(...a);
  let sw = ctx.serviceWorkers()[0] || await ctx.waitForEvent('serviceworker', { timeout: 15000 });
  log('SW', sw.url());
  const page = await ctx.newPage();
  for (const u of urls) {
    await page.goto(u.replace('PORT', port)).catch(e => log('goto err', e.message));
    await page.bringToFront();
    log('NAV', new URL(u.replace('PORT', port)).hostname);
    await new Promise(r => setTimeout(r, u.includes('hold') ? 6000 : 3500));
  }
  await new Promise(r => setTimeout(r, 1500));
  await ctx.close();
})().catch(e => { console.error('FAIL', e); process.exit(1); });
