import {contextFacts} from "./enrichment-format.js";
export const editionNames = {"dnd-0":"D&D 0","dnd-bx":"B/X","adnd-1":"AD&D 1","adnd-2":"AD&D 2","dnd-3":"D&D 3","dnd-35":"D&D 3.5","dnd-4":"D&D 4","dnd-5":"D&D 5","dnd-55":"D&D 5.5"};
const sourceName = source => source === "dmsguild" ? "DM’s Guild" : "DriveThruRPG";
const officialName = value => value === true ? "Official" : value === false ? "Non-official" : "Official status unknown";
const release = value => value ? (/^\d{4}-\d{2}-\d{2}(?:$|[ T])/.test(value) ? value.slice(0,10) : value) : "Release date unknown";
const node = (tag, text, cls) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (cls) el.className = cls; return el; };
export function booksUI({api, getState}) {
  const $ = id => document.getElementById(id);
  let active = false, dirty = true, folder = "", signature = "", serial = 0, timer, page = 0, cursors = [""], next = "", selectedBook, scroll = 0, lastButton;
  const canRead = () => Boolean(api && getState().storageAvailable && !getState().loading && !getState().closing && !getState().maintenance?.needsSchema);
  const query = () => ({source:$("bookSource").value, edition:$("bookEdition").value, official:$("bookOfficial").value, sort:$("bookSort").value, search:$("bookSearch").value.trim(), searchIn:$("bookSearchIn").value, after:cursors[page]});
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
    $("bookStatus").textContent="Searching the archives…"; $("bookList").setAttribute("aria-busy","true");
    try {
      const result=await api.BrowseBooks(query());
      if(request!==serial || requestedFolder!==getState().dataFolder) return;
      for(const book of result.books || []) {
        const li=node("li"),button=node("button",undefined,"book-row"); button.type="button";
        button.append(image(book,result.catalogKey));
        const title=node("span",undefined,"book-row-main");
        title.append(node("strong",book.name,"book-name"),node("span",[book.publisher,sourceName(book.source)].filter(Boolean).join(" · "),"book-publisher"),badges(book));
        button.append(title,node("span",release(book.releaseDate),"book-date"),node("span","›","book-arrow"));
        button.onclick=()=>{lastButton=button;void detail(book);}; li.append(button);$("bookList").append(li);
      }
      next=result.next || "";$("bookEmpty").hidden=Boolean(result.books?.length);
      $("bookStatus").textContent=`${result.books?.length || 0} books on this page · ${$("bookSource").value ? sourceName($("bookSource").value) : "Both storefronts"}`;
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
  $("bookSearch").oninput=()=>{serial++;clearTimeout(timer);timer=setTimeout(resetPage,350);};
  $("bookReset").onclick=()=>{$("bookSearch").value="";$("bookSource").value="";$("bookEdition").value="";$("bookOfficial").value="all";$("bookSort").value="name-asc";$("bookSearchIn").value="name";resetPage();};
  $("bookRefresh").onclick=resetPage;
  $("booksNext").onclick=()=>{if(next){cursors[++page]=next;void load();$("workspace").scrollTop=0;}};
  $("booksPrevious").onclick=()=>{if(page){page--;void load();$("workspace").scrollTop=0;}};
  return {
    activate(value){active=value;if(active && dirty && !selectedBook)void load();},
    render(s){
      if(folder!==s.dataFolder){folder=s.dataFolder;serial++;dirty=true;page=0;cursors=[""];selectedBook=null;$("bookList").replaceChildren();$("bookDetail").replaceChildren();$("bookDetailView").hidden=true;$("bookListView").hidden=false;}
      const m=s.maintenance || {},pending=m.catalog?.pending || 0;
      $("bookGate").hidden=!(m.needsSchema || m.countsStale || pending || !s.storageAvailable);
      $("bookGateMessage").textContent=m.needsSchema ? "Apply the database update, then build the D&D browsing index in Data maintenance." : !s.storageAvailable ? "Open a local catalog to browse its D&D collection." : m.countsStale ? "Index counts are out of date. Refresh counts in Data maintenance; indexed books remain available." : `${pending.toLocaleString()} saved responses still need indexing. The library shows currently indexed D&D products.`;
      const newSignature=JSON.stringify([s.loading,s.maintenance?.needsSchema,s.running,m.running,m.catalog?.current,m.catalog?.pending]);
      if(signature!==newSignature){signature=newSignature;dirty=true;}
      for(const control of $("bookFilters").querySelectorAll("input,select,button"))control.disabled=!canRead();
      $("bookRefresh").disabled=!canRead();
      if(active && dirty && !selectedBook && !s.loading && !m.running && !s.running)void load();
    }
  };
}
