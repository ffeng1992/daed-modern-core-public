// Synthetic local browser/API check. Does not enable the datapath.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
const temporary = await mkdtemp(join(tmpdir(), 'bridge-browser-'));
const endpoint = 'http://127.0.0.1:22323/graphql';
const oldEndpoint = 'http://127.0.0.1:9/graphql';
const username = 'fixture';
const password = 'Synthetic12345';
const isOperation = (response, name) =>
  response.url() === endpoint && response.request().postData()?.includes(`query ${name}`);
const readOperation = async (response, name) => {
  assert.equal(response.status(), 200, `${name} HTTP status`);
  const body = await response.json();
  assert.equal(body.errors, undefined, `${name} GraphQL errors`);
  return body.data;
};
const children = [];
let browser;
let page;
const errors = [];
const consoleErrors = [];
const failedRequests = [];
try {
  await mkdir('.artifacts', {recursive: true});
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
  page = await browser.newPage({locale:'en-US'});
  page.on('pageerror', e=>errors.push(e.message));
  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('requestfailed', request => {
    failedRequests.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText}`);
  });
  await page.goto('http://127.0.0.1:4199/#/setup');
  await page.evaluate(value => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem('endpointURL', value);
  }, oldEndpoint);
  await page.reload();
  assert.equal(await page.locator('form input').inputValue(), oldEndpoint, 'initial endpoint must differ');
  console.log('PASS: cleared browser storage; setup starts from a different endpoint');
  await page.locator('form input').fill(endpoint);
  const firstNumberUsers = page.waitForResponse(response => isOperation(response, 'NumberUsers'));
  await page.locator('form button[type=submit]').click();
  assert.equal((await readOperation(await firstNumberUsers, 'NumberUsers')).numberUsers, 0);
  await page.getByPlaceholder('admin').waitFor();
  assert.equal(await page.locator('form input').count(), 2, 'account creation form visible');
  console.log('PASS: numberUsers=0 advances to account creation without resetting step 1');
  await page.getByPlaceholder('admin').fill(username);
  await page.getByPlaceholder('password',{exact:true}).fill(password);
  await page.locator('form button[type=submit]').click();
  await page.getByRole('button',{name:'Login',exact:true}).waitFor();
  await page.getByPlaceholder('admin').fill(username);
  await page.getByPlaceholder('password',{exact:true}).fill(password);
  await page.locator('form button[type=submit]').click();
  await page.getByRole('link',{name:/start your journey/i}).waitFor();
  console.log('PASS: synthetic account creation and login');
  const firstUser = page.waitForResponse(response => isOperation(response, 'User'));
  await page.getByRole('link',{name:/start your journey/i}).click();
  assert.equal((await readOperation(await firstUser, 'User')).user.username, username);
  await page.locator('[data-testid="section"]').first().waitFor();
  const refreshedUser = page.waitForResponse(response => isOperation(response, 'User'));
  await page.reload();
  assert.equal((await readOperation(await refreshedUser, 'User')).user.username, username);
  await page.locator('[data-testid="section"]').first().waitFor();
  console.log('PASS: dashboard reads management data after login and refresh');
  await page.getByRole('button', {name: username}).click();
  await page.getByRole('menuitem', {name: /logout/i}).click();
  await page.getByRole('button', {name: 'Continue', exact: true}).waitFor();
  assert.equal(await page.evaluate(() => localStorage.getItem('token')), '', 'logout clears identity');
  const existingNumberUsers = page.waitForResponse(response => isOperation(response, 'NumberUsers'));
  await page.locator('form button[type=submit]').click();
  assert.equal((await readOperation(await existingNumberUsers, 'NumberUsers')).numberUsers, 1);
  await page.getByPlaceholder('admin').fill(username);
  await page.getByPlaceholder('password', {exact: true}).fill(password);
  await page.locator('form button[type=submit]').click();
  await page.getByRole('link', {name: /start your journey/i}).waitFor();
  const reloginUser = page.waitForResponse(response => isOperation(response, 'User'));
  await page.getByRole('link', {name: /start your journey/i}).click();
  const reloginResponse = await reloginUser;
  assert.match(reloginResponse.request().headers().authorization ?? '', /^Bearer \S+$/, 'new identity request');
  assert.equal((await readOperation(reloginResponse, 'User')).user.username, username);
  await page.locator('[data-testid="section"]').first().waitFor();
  if(errors.length) throw new Error('Browser page error count: '+errors.length);
  assert.deepEqual(consoleErrors, [], 'browser console errors');
  assert.deepEqual(failedRequests, [], 'failed browser requests');
  console.log('PASS: logout and relogin fetch current user through a fresh authenticated connection (API-only)');
} catch (error) {
  if (page) {
    try { await page.screenshot({path: '.artifacts/browser-setup-failure.png', fullPage: true}); }
    catch (captureError) { console.error('Screenshot capture failed:', String(captureError)); }
    console.error('Browser regression failed:', {
      pageURL: page.url(),
      visibleText: (await page.locator('body').innerText()).slice(0, 2000),
      pageErrors: errors,
      consoleErrors,
      failedRequests,
    });
  }
  throw error;
} finally {
  await browser?.close();
  for(const p of children) p.kill('SIGTERM');
  await Promise.all(children.map(p=>p.exitCode!==null ? Promise.resolve() : new Promise(r=>{p.once('exit',r);setTimeout(()=>{p.kill('SIGKILL');r()},5000).unref()})));
  await rm(temporary,{recursive:true,force:true});
}
