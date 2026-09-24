// Synthetic local browser/API check. Does not enable the datapath.
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
const temporary = await mkdtemp(join(tmpdir(), 'bridge-browser-'));
const children = [];
let browser;
try {
  children.push(spawn(resolve('.artifacts/daed-browser-api'), ['run','--api-only','--config',temporary,'--listen','127.0.0.1:22323'], {stdio:'ignore'}));
  children.push(spawn('python3',['-m','http.server','4199','--bind','127.0.0.1','--directory','apps/web/dist'], {stdio:'ignore'}));
  for (let i=0; i<100; i++) {
    if (children.some(p=>p.exitCode!==null)) throw new Error('Fixture process exited');
    try {
      const r = await fetch('http://127.0.0.1:22323/graphql', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query:'{numberUsers}'})});
      if (r.ok && (await fetch('http://127.0.0.1:4199/')).ok) break;
    } catch {}
    if(i===99) throw new Error('Fixture startup timeout');
    await new Promise(r=>setTimeout(r,100));
  }
  browser = await chromium.launch({headless:true});
  const page = await browser.newPage({locale:'en-US'});
  const errors=[];
  page.on('pageerror', e=>errors.push(e.message));
  await page.goto('http://127.0.0.1:4199/#/setup');
  await page.locator('form input').fill('http://127.0.0.1:22323/graphql');
  await page.locator('form button[type=submit]').click();
  await page.getByPlaceholder('admin').fill('fixture');
  await page.getByPlaceholder('password',{exact:true}).fill('Synthetic12345');
  await page.locator('form button[type=submit]').click();
  await page.getByRole('button',{name:'Login',exact:true}).waitFor();
  await page.getByPlaceholder('admin').fill('fixture');
  await page.getByPlaceholder('password',{exact:true}).fill('Synthetic12345');
  await page.locator('form button[type=submit]').click();
  await page.getByRole('link',{name:/start your journey/i}).click();
  await page.locator('[data-testid="section"]').first().waitFor();
  await page.reload();
  await page.locator('[data-testid="section"]').first().waitFor();
  if(errors.length) throw new Error('Browser page error count: '+errors.length);
  console.log('PASS: actual browser endpoint setup, first user, login, dashboard and reload persistence (API-only)');
} finally {
  await browser?.close();
  for(const p of children) p.kill('SIGTERM');
  await Promise.all(children.map(p=>p.exitCode!==null ? Promise.resolve() : new Promise(r=>{p.once('exit',r);setTimeout(()=>{p.kill('SIGKILL');r()},5000).unref()})));
  await rm(temporary,{recursive:true,force:true});
}
