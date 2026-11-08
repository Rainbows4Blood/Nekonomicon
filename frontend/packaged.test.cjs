// Invoked by TestPackagedFrontendBrowser with the Go executable's embedded files.
const {chromium}=require(process.env.NEKONOMICON_PLAYWRIGHT);
const assert=require('node:assert/strict');
const fs=require('node:fs');
const files=JSON.parse(fs.readFileSync(0,'utf8'));
(async()=>{
  const browser=await chromium.launch({headless:true});
  try {
    for(const width of [1440,390]) for(const mode of ['ready','loading','missing-module']) {
      const page=await browser.newPage({viewport:{width,height:900}});
      const errors=[],missing=[];
      page.on('pageerror',err=>errors.push(err.message));
      await page.route('https://nekonomicon.test/**',async route=>{
        const name=new URL(route.request().url()).pathname.slice(1)||'index.html';
        if(!files[name] || mode==='missing-module'&&name==='enrichment-format.js') {
          missing.push(name);await route.fulfill({status:404,body:'Not found'});return;
        }
        await route.fulfill({body:Buffer.from(files[name],'base64'),contentType:name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.png')?'image/png':'text/html'});
      });
      await page.addInitScript(mode=>{
        window.aborts=0;window.snapshotReads=0;
        window.fixture={source:'dtrpg',dataFolder:'Synthetic catalog',storageAvailable:true,storageReady:true,state:'ready',loading:mode==='loading',maintenance:{catalog:{pending:0}},openAI:{accounts:[]}};
        window.go={main:{App:{Snapshot:async()=>{window.snapshotReads++;return window.fixture},Profiles:async()=>[],Failures:async()=>[],AbortAndClose:async()=>{window.aborts++},BrowseBooks:async()=>({books:[]})}}};
        window.runtime={EventsOn:(_,callback)=>{window.emit=callback}};
      },mode);
      await page.goto('https://nekonomicon.test/');
      if(mode==='ready') {
        await page.locator('#loadingOverlay').waitFor({state:'hidden'});
        assert(await page.evaluate(()=>window.snapshotReads>0));
        await page.locator('#dndTab').click();
        await page.locator('#enrichmentSubTab').click();
        assert(await page.locator('#enrichStart').isVisible());
      } else {
        if(mode==='missing-module') {
          await page.waitForFunction(()=>document.getElementById('loadingTitle').textContent==='Could not start the interface');
          assert.deepEqual(missing,['enrichment-format.js']);
          assert(await page.locator('.loading-spinner').isHidden());
        } else await page.waitForFunction(()=>window.snapshotReads>0);
        assert(await page.locator('#abortLoading').isEnabled());
        await page.locator('#abortLoading').click();
        await page.waitForFunction(()=>window.aborts===1);
        assert(await page.locator('#abortLoading').isDisabled());
      }
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
      assert.deepEqual(errors,[]);
      if(mode!=='missing-module')assert.deepEqual(missing,[]);
      await page.close();
    }
    console.log('Embedded UI: startup, transitive imports, loading abort and missing-module recovery pass at desktop/narrow widths.');
  } finally {await browser.close()}
})().catch(err=>{console.error(err);process.exitCode=1});
