import {contextFacts} from "./enrichment-format.js";
export const editionNames = {"dnd-0":"D&D 0","dnd-bx":"B/X","adnd-1":"AD&D 1","adnd-2":"AD&D 2","dnd-3":"D&D 3","dnd-35":"D&D 3.5","dnd-4":"D&D 4","dnd-5":"D&D 5","dnd-55":"D&D 5.5"};
const sourceName = source => source === "dmsguild" ? "DM’s Guild" : "DriveThruRPG";
const officialName = value => value === true ? "Official" : value === false ? "Non-official" : "Official status unknown";
const release = value => value ? (/^\d{4}-\d{2}-\d{2}(?:$|[ T])/.test(value) ? value.slice(0,10) : value) : "Release date unknown";
const node = (tag, text, cls) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (cls) el.className = cls; return el; };
export function booksUI({api, getState, adventuresOnly=false}) {
  const $ = id => document.getElementById(adventuresOnly ? id.replace(/^book(?!Gate)/,"adventure") : id);
  let active = false, dirty = true, folder = "", signature = "", serial = 0, timer, page = 0, cursors = [""], next = "", selectedBook, scroll = 0, lastButton;
  const canRead = () => Boolean(api && getState().storageAvailable && !getState().loading && !getState().closing && !getState().maintenance?.needsSchema);
  const numberFields=adventuresOnly?["MinPlayer","MaxPlayer","MinLevel","MaxLevel"]:[],stateFields=adventuresOnly?["Players","Levels","Settings","Locations","Dates"]:[],choiceFields=adventuresOnly?["Setting","Location","WorldDate","Calendar"]:[];
  const lowerFirst=s=>s[0].toLowerCase()+s.slice(1);
  const choices=Object.fromEntries(choiceFields.map(f=>[f,{selected:new Map(),options:[],cursors:[""],page:0,next:"",serial:0,busy:false,error:false,loaded:false}]));
  let choicesDirty=true;
  const choiceKinds={Setting:"setting",Location:"location",WorldDate:"world-date",Calendar:"calendar"};
  function choiceControls(f){
    const c=choices[f],ready=canRead();$("book"+f).disabled=!ready||c.busy;
    for(const [action,disabled] of [["Previous",!c.page],["Next",!c.next],["Clear",!c.selected.size],["Retry",false]])$("book"+f+"Choices"+action).disabled=!ready||c.busy||disabled;
    $("book"+f+"ChoicesRetry").hidden=!c.error;
  }
  function paintChoices(f){
    const c=choices[f],select=$("book"+f);select.replaceChildren();
    const visible=new Set(c.options.map(o=>o.value));
    const pinned=[...c.selected].filter(([value])=>!visible.has(value));
    const append=(parent,o)=>{const option=node("option",o.label);option.value=o.value;option.selected=c.selected.has(o.value);option.title=o.label;parent.append(option);};
    if(pinned.length){const group=node("optgroup");group.label="Selected from other pages";for(const [value,label]of pinned)append(group,{value,label});select.append(group);}
    for(const o of c.options)append(select,o);
    $("book"+f+"ChoicesStatus").textContent=c.busy?"Loading values…":c.error?"Could not load values. Retry to try again.":c.loaded?c.options.length?`Page ${c.page+1} · ${c.selected.size} selected`:"No saved values yet.":"Values load when Adventures opens.";
    choiceControls(f);
  }
  async function loadChoices(f,index=0){
    if(!canRead())return;const c=choices[f],ticket=++c.serial,requestedFolder=getState().dataFolder;
    c.busy=true;c.error=false;c.requested=index;paintChoices(f);
    try{const result=await api.AdventureFilterOptions(choiceKinds[f],c.cursors[index]||"");
      if(ticket!==c.serial||requestedFolder!==getState().dataFolder||!canRead())return;
      c.options=result.options||[];c.next=result.next||"";c.page=index;c.loaded=true;
    }catch{if(ticket===c.serial)c.error=true;}
    finally{if(ticket===c.serial){c.busy=false;paintChoices(f);}}
  }
  function refreshChoices(){if(!adventuresOnly||!active||!canRead()||!choicesDirty)return;choicesDirty=false;for(const f of choiceFields){choices[f].cursors=[""];choices[f].page=0;choices[f].next="";void loadChoices(f);}}
  function clearChoices(){for(const f of choiceFields){choices[f].selected.clear();paintChoices(f);}}
  for(const f of choiceFields){
    $("book"+f).onchange=()=>{const selected=Array.from($("book"+f).querySelectorAll("option")).filter(o=>o.selected);
      if(selected.length>64){paintChoices(f);$("book"+f+"ChoicesStatus").textContent="Select at most 64 values.";return;}
      choices[f].selected=new Map(selected.map(o=>[o.value,o.textContent]));choiceControls(f);$("book"+f+"ChoicesStatus").textContent=`Page ${choices[f].page+1} · ${selected.length} selected`;resetPage();
    };
    $("book"+f+"ChoicesPrevious").onclick=()=>{const c=choices[f];if(c.page)void loadChoices(f,c.page-1);};
    $("book"+f+"ChoicesNext").onclick=()=>{const c=choices[f];if(c.next){c.cursors[c.page+1]=c.next;void loadChoices(f,c.page+1);}};
    $("book"+f+"ChoicesClear").onclick=()=>{choices[f].selected.clear();paintChoices(f);resetPage();};
    $("book"+f+"ChoicesRetry").onclick=()=>{void loadChoices(f,choices[f].requested??choices[f].page);};
  }
  const enrichmentQuery=()=>{const q={};for(const f of numberFields)q[lowerFirst(f)]={from:$("book"+f+"From").value===""?null:Number($("book"+f+"From").value),to:$("book"+f+"To").value===""?null:Number($("book"+f+"To").value)};for(const f of stateFields)q[lowerFirst(f)]=$("book"+f+"State").value;for(const f of choiceFields)q[lowerFirst(f)]=[...choices[f].selected.keys()].sort();return q;};
  const enrichmentControls=[...numberFields.flatMap(f=>["book"+f+"From","book"+f+"To"]),...stateFields.map(f=>"book"+f+"State")];
  const query = () => ({adventuresOnly,enrichment:enrichmentQuery(),source:$("bookSource").value, edition:$("bookEdition").value, official:$("bookOfficial").value, sort:$("bookSort").value, search:$("bookSearch").value.trim(), searchIn:$("bookSearchIn").value, after:cursors[page]});
  function image(book,key,large=false) {
    const cover = node("span",undefined,large ? "book-cover book-cover-large" : "book-cover");
    cover.append(node("span","✦","cover-fallback"));
    if (book.hasCover) {
      const img = document.createElement("img"); img.alt = large ? `Cover of ${book.name}` : ""; img.loading="lazy"; img.decoding="async";
      const params = new URLSearchParams({catalog:key,source:book.source,id:String(book.id),size:large?"detail":"small"});
      img.src = `/catalog/cover?${params}`;
      img.onerror = () => { img.remove(); cover.title="Cover unavailable locally"; };
      cover.append(img);
    } else cover.title = "No saved cover";
    return cover;
  }
  function badges(book) {
    const tags=node("span",undefined,"book-tags");
    for (const id of book.editions || []) tags.append(node("span",editionNames[id] || id,"edition-tag"));
    tags.append(node("span",officialName(book.official),"official-tag")); return tags;
  }
  function pagination(loading=false) { $("booksPrevious").disabled = loading || page===0; $("booksNext").disabled = loading || !next; $("bookPage").textContent=`Page ${page+1}`; }
  async function load() {
    clearTimeout(timer); dirty=false;
    const request=++serial, requestedFolder=getState().dataFolder;
    $("bookList").replaceChildren(); $("bookEmpty").hidden=true; next=""; pagination(true);
    if (!canRead()) { $("bookStatus").textContent="Prepare your local catalog in Data maintenance to browse books."; return; }
    if(!$("bookFilters").checkValidity?.() && $("bookFilters").checkValidity){$("bookFilters").reportValidity();$("bookStatus").textContent="Check the numeric filters.";return;}
    for(const f of numberFields){const from=$("book"+f+"From").value,to=$("book"+f+"To").value;if(from!==""&&to!==""&&Number(from)>Number(to)){$("bookStatus").textContent="Each numeric From must be less than or equal to To.";return;}}
    if(adventuresOnly)$("bookFilterCount").textContent=(enrichmentControls.filter(id=>$(id).value!=="").length+choiceFields.filter(f=>choices[f].selected.size).length) ? `(${(enrichmentControls.filter(id=>$(id).value!=="").length+choiceFields.filter(f=>choices[f].selected.size).length)} active)` : "";
    if(adventuresOnly)for(const heading of document.querySelectorAll("#adventuresSubPanel th[data-sort]")){const sort=$("bookSort").value,key=heading.dataset.sort;heading.setAttribute("aria-sort",sort===key+"-asc"?"ascending":sort===key+"-desc"?"descending":"none");}
    $("bookStatus").textContent="Searching the archives…"; $("bookList").setAttribute("aria-busy","true");
    try {
      const result=await api.BrowseBooks(query());
      if(request!==serial || requestedFolder!==getState().dataFolder) return;
      for(const book of result.books || []) {
        const li=node(adventuresOnly?"tr":"li"),button=node("button",undefined,"book-row"); button.type="button";
        button.append(image(book,result.catalogKey));
        const title=node("span",undefined,"book-row-main");
        title.append(node("strong",book.name,"book-name"),node("span",[book.publisher,sourceName(book.source)].filter(Boolean).join(" · "),"book-publisher"),badges(book));
        button.append(title,node("span",release(book.releaseDate),"book-date"),node("span","›","book-arrow"));
        button.onclick=()=>{lastButton=button;void detail(book);};
        if(adventuresOnly){
          button.className="adventure-book";button.replaceChildren(image(book,result.catalogKey),title);
          const nameCell=node("td");nameCell.append(button);li.append(nameCell);
          for(const key of ["players","levels"])for(const endpoint of ["min","max"])li.append(node("td",book.enrichment?.[key]?.[endpoint]??(book[key+"Enriched"]?"Unknown":"Not enriched yet"),"adventure-number"));
          for(const f of contextFacts(book.enrichment,{settings:book.settingsEnriched,locations:book.locationsEnriched,dates:book.datesEnriched}))li.append(node("td",f.value));
          const calendars=[...new Set((book.enrichment?.dates?.items||[]).map(d=>d.calendar||"Unknown"))];
          li.append(node("td",calendars.length?calendars.join("; "):book.datesEnriched?"Unknown":"Not enriched yet"),node("td",release(book.releaseDate)));
        }else li.append(button);
        $("bookList").append(li);
      }
      next=result.next || "";$("bookEmpty").hidden=Boolean(result.books?.length);
      $("bookStatus").textContent=`${result.books?.length || 0} ${adventuresOnly?"adventures":"books"} on this page · ${$("bookSource").value ? sourceName($("bookSource").value) : "Both storefronts"}`;
    } catch {
      if(request===serial) $("bookStatus").textContent="Could not load books. Try a shorter search, refresh, or check Data maintenance.";
    } finally { if(request===serial) {pagination();$("bookList").removeAttribute("aria-busy");} }
  }
  function resetPage() { page=0;cursors=[""];next="";dirty=true;if(active)void load(); }
  function section(title,entries) {
    if(!entries?.length)return;
    const part=node("section",undefined,"book-detail-section");part.append(node("h3",title));
    const dl=node("dl",undefined,"book-facts");for(const entry of entries){dl.append(node("dt",entry.label),node("dd",entry.value || "—"));}part.append(dl);return part;
  }
  async function detail(book) {
    selectedBook=book;scroll=$("workspace").scrollTop;$("bookListView").hidden=true;$("bookDetailView").hidden=false;$("bookDetail").replaceChildren();$("bookDetailStatus").textContent="Opening this tome…";$("workspace").scrollTop=0;$("bookBack").focus();
    const request=++serial,requestedFolder=getState().dataFolder;
    try {
      const d=await api.BookDetails(book.source,book.id);
      if(request!==serial || requestedFolder!==getState().dataFolder)return;
      $("bookDetailStatus").textContent="";
      const hero=node("header",undefined,"book-detail-heading"),title=node("div");
      const heading=node("h2",d.name);heading.tabIndex=-1;
      title.append(node("span",sourceName(d.source),"eyebrow"),heading,node("p",d.publisher || "Publisher not provided","book-publisher"),badges(d),node("p",`Release date · ${release(d.releaseDate)}`,"book-release"));
      const open=node("button","View on storefront","primary");open.onclick=async()=>{try{await api.OpenBook(d.source,d.id);}catch{$("bookDetailStatus").textContent="Could not open the storefront link.";}};
      title.append(open);hero.append(image(d,d.catalogKey,true),title);$("bookDetail").append(hero);
      const body=node("section",undefined,"book-description");body.append(node("h3","Within these pages"));
      body.append(node("p",d.description || "No description is available in the saved response.","description-text"));
      $("bookDetail").append(body);
      const extras=node("div",undefined,"book-detail-grid");
      const aiFacts=[];for(const [key,label] of [["players","Players (excluding GM)"],["levels","Character levels"]]){const r=d.enrichment?.[key];if(r&&(r.min!=null||r.max!=null||d[key+"Enriched"])){aiFacts.push({label,value:r.min==null&&r.max==null?"Unknown (enriched)":`${r.min ?? "Unknown"}–${r.max ?? "Unknown"}`},{label:"Evidence",value:[r.evidence,...(r.sources||[])].filter(Boolean).join(" · ")});}}
      for(const fact of contextFacts(d.enrichment,{settings:d.settingsEnriched,locations:d.locationsEnriched,dates:d.datesEnriched})){
        if(fact.value!=="Not enriched yet"||aiFacts.length)aiFacts.push({label:fact.label,value:fact.value},{label:"Evidence",value:[fact.evidence,...fact.sources].filter(Boolean).join(" · ")});
      }
      const aiSection=section("Adventure enrichment",aiFacts);if(aiSection)extras.append(aiSection);
      for(const item of [section("Book details",d.facts),section("Creators",d.creators),section("Formats",d.formats),section("Files",d.files)])if(item)extras.append(item);
      $("bookDetail").append(extras);
      if(d.warnings?.length){const details=node("details",undefined,"book-source-notes");details.append(node("summary","Notes about the saved source data"));const list=node("ul");for(const warning of d.warnings)list.append(node("li",warning));details.append(list);$("bookDetail").append(details);}
      $("bookDetail").append(node("p",`${sourceName(d.source)} · Product ${d.id} · Saved ${d.fetched}`,"book-provenance"));
      heading.focus();
    } catch {if(request===serial)$("bookDetailStatus").textContent="This book could not be opened. Return to the library and refresh.";}
  }
  $("bookBack").onclick=()=>{serial++;selectedBook=null;$("bookListView").hidden=false;$("bookDetailView").hidden=true;$("workspace").scrollTop=scroll;if(dirty)void load();else lastButton?.focus();};
  $("bookFilters").onsubmit=event=>{event.preventDefault();resetPage();};
  for(const id of ["bookSource","bookEdition","bookOfficial","bookSort","bookSearchIn"])$(id).onchange=resetPage;
  for(const id of enrichmentControls)$(id).onchange=resetPage;
  $("bookSearch").oninput=()=>{serial++;clearTimeout(timer);timer=setTimeout(resetPage,350);};
  $("bookReset").onclick=()=>{$("bookSearch").value="";$("bookSource").value="";$("bookEdition").value="";$("bookOfficial").value="all";$("bookSort").value="name-asc";$("bookSearchIn").value="name";for(const id of enrichmentControls)$(id).value="";clearChoices();resetPage();};
  if(adventuresOnly)for(const heading of document.querySelectorAll("#adventuresSubPanel th[data-sort]"))heading.querySelector("button").onclick=()=>{const key=heading.dataset.sort;$("bookSort").value=key+($("bookSort").value===key+"-asc"?"-desc":"-asc");resetPage();};
  $("bookRefresh").onclick=()=>{choicesDirty=true;refreshChoices();resetPage();};
  $("booksNext").onclick=()=>{if(next){cursors[++page]=next;void load();$("workspace").scrollTop=0;}};
  $("booksPrevious").onclick=()=>{if(page){page--;void load();$("workspace").scrollTop=0;}};
  return {
    activate(value){active=value;refreshChoices();if(active && dirty && !selectedBook)void load();},
    render(s){
      if(folder!==s.dataFolder){folder=s.dataFolder;serial++;dirty=true;choicesDirty=true;for(const f of choiceFields){const c=choices[f];c.serial++;c.selected.clear();c.options=[];c.cursors=[""];c.page=0;c.next="";c.busy=false;c.loaded=false;c.error=false;paintChoices(f);}page=0;cursors=[""];selectedBook=null;$("bookList").replaceChildren();$("bookDetail").replaceChildren();$("bookDetailView").hidden=true;$("bookListView").hidden=false;}
      const m=s.maintenance || {},pending=m.catalog?.pending || 0;
      $("bookGate").hidden=!(m.needsSchema || m.countsStale || pending || !s.storageAvailable);
      $("bookGateMessage").textContent=m.needsSchema ? "Apply the database update, then build the D&D browsing index in Data maintenance." : !s.storageAvailable ? "Open a local catalog to browse its D&D collection." : m.countsStale ? "Index counts are out of date. Refresh counts in Data maintenance; indexed books remain available." : `${pending.toLocaleString()} saved responses still need indexing. The library shows currently indexed D&D products.`;
      const newSignature=JSON.stringify([s.loading,s.maintenance?.needsSchema,s.running,m.running,m.catalog?.current,m.catalog?.pending,s.enrichment?.running]);
      if(signature!==newSignature){signature=newSignature;dirty=true;choicesDirty=true;}
      for(const control of $("bookFilters").querySelectorAll("input,select,button"))control.disabled=!canRead();
      $("bookRefresh").disabled=!canRead();
      for(const f of choiceFields)choiceControls(f);
      refreshChoices();
      if(active && dirty && !selectedBook && !s.loading && !m.running && !s.running)void load();
    }
  };
}
