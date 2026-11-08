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
        window.go={main:{App:{Snapshot:async()=>{window.snapshotReads++;return window.fixture},Profiles:async()=>[],Failures:async()=>[],AbortAndClose:async()=>{window.aborts++},AdventureFilterOptions:async kind=>({options:({setting:[{value:'eberron',label:'Eberron'},{value:'forgotten realms',label:'Forgotten Realms'}],location:[],calendar:[],"world-date":[]})[kind],next:''}),BrowseBooks:async q=>{window.bookQuery=q;return {books:[{source:'dtrpg',id:1,name:'A long adventure title',publisher:'Fixture publisher',editions:['dnd-5'],enrichment:{players:{min:3,max:5},levels:{min:1,max:4},settings:{names:['Forgotten Realms','Eberron'],agnostic:true},locations:{items:[{name:'Waterdeep',setting:'Forgotten Realms'}]},dates:{items:[{date:'Winter 1492 DR',calendar:'Dalereckoning',setting:'Forgotten Realms'}]}},playersEnriched:true,levelsEnriched:true,settingsEnriched:true,locationsEnriched:true,datesEnriched:true}]}}}}};
        window.runtime={EventsOn:(_,callback)=>{window.emit=callback}};
      },mode);
      await page.goto('https://nekonomicon.test/');
      if(mode==='ready') {
        await page.locator('#loadingOverlay').waitFor({state:'hidden'});
        assert(await page.evaluate(()=>window.snapshotReads>0));
        await page.locator('#dndTab').click();
        assert(await page.locator('#librarySubPanel').isVisible());assert.equal(await page.locator('#bookSort option').count(),4);
        await page.locator('#adventuresSubTab').click();
        await page.waitForFunction(()=>window.bookQuery?.adventuresOnly===true);
        assert.equal(await page.locator('#adventureList tr td').count(),10);
        await page.locator('.enriched-filters summary').click();
        await page.locator('#adventureSetting option').first().waitFor({state:'attached'});await page.locator('#adventureSetting').selectOption(['eberron','forgotten realms']);
        await page.locator('#adventureSort').selectOption('setting-desc');
        await page.waitForFunction(()=>window.bookQuery?.enrichment.setting?.length===2&&window.bookQuery.sort==='setting-desc');
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
        await page.locator('#adventureMinLevelFrom').focus();await page.keyboard.press('Tab');assert(await page.locator('#adventureMinLevelTo').evaluate(e=>e===document.activeElement));
        await page.locator('#adventuresSubPanel th[data-sort="min-player"] button').click();
        await page.waitForFunction(()=>window.bookQuery.sort==='min-player-asc');
        assert.equal(await page.locator('#adventuresSubPanel th[data-sort="min-player"]').getAttribute('aria-sort'),'ascending');
        await page.locator('#librarySubTab').click();assert(await page.locator('#librarySubPanel').isVisible());
        await page.locator('#adventuresSubTab').click();assert.deepEqual(await page.locator('#adventureSetting').evaluate(el=>Array.from(el.selectedOptions,o=>o.value)),['eberron','forgotten realms']);
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
