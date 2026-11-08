import {contextFacts} from "./enrichment-format.js";
const range = r => r?.min == null && r?.max == null ? "Unknown" : `${r?.min ?? "Unknown"}–${r?.max ?? "Unknown"}`;
const node = (tag,text) => { const el=document.createElement(tag); el.textContent=text;return el; };
export function enrichmentUI({api,getState,invoke,books,adventures}) {
  const $=id=>document.getElementById(id);
  let active=false,section="library",signature="",serial=0,page=0,folder="",pending;
  const ready=()=>api && getState().storageAvailable && !getState().maintenance?.needsSchema && !getState().loading;
  async function load(){
    if(!active||section!=="enrichment"||!ready())return;
    const ticket=++serial,requestedFolder=getState().dataFolder;
    $("enrichError").textContent="";
    try {
      const d=await api.EnrichmentDashboard(page*30);
      if(ticket!==serial||requestedFolder!==getState().dataFolder)return;
      const c=d.counts||{};pending=c.pending;
      for(const [id,key] of [["Eligible","adventures"],["Players","players"],["Levels","levels"],["Settings","settings"],["Locations","locations"],["Dates","dates"],["Pending","pending"]])$("enrich"+id).textContent=Number(c[key]||0).toLocaleString();
      $("enrichCoverNote").textContent=`${c.missingCovers||0} adventures have no recorded cover. These books can be enriched without an image. ${c.unknown||0} books are enriched with some values unknown. ${c.failed||0} books have a saved issue.`;
      $("enrichResults").replaceChildren();
      for(const b of d.books||[]){
        const li=node("li",""),heading=node("h4",b.name),meta=node("p",`${b.source==="dmsguild"?"DM’s Guild":"DriveThruRPG"} · ${b.status==="complete"?"Enriched":b.status==="failed"?"Needs attention":"In progress — saved partial result"}`);
        li.append(heading,meta,node("p",`Players: ${range(b.result?.players)} · Character levels: ${range(b.result?.levels)}`));
        const context=contextFacts(b.result,{settings:b.settingsDone,locations:b.locationsDone,dates:b.datesDone});
        for(const fact of context)li.append(node("p",fact.label+": "+fact.value));
        if(b.message)li.append(node("p",b.message));
        const evidence=node("details","");evidence.append(node("summary","Evidence"));
        for(const [key,label] of [["players","Players"],["levels","Levels"]]){const r=b.result?.[key];evidence.append(node("p",`${label}: ${r?.evidence||"No supported value found."}`));for(const url of r?.sources||[]){const p=node("p",url);p.className="enrich-source";evidence.append(p);}}
        for(const fact of context){evidence.append(node("p",fact.label+": "+(fact.evidence||fact.value)));for(const url of fact.sources)evidence.append(node("p",url));}
        li.append(evidence);$("enrichResults").append(li);
      }
      if(!d.books?.length)$("enrichResults").append(node("li","No enrichment results on this page yet."));
      $("enrichPage").textContent=`Page ${page+1}`;$("enrichPrevious").disabled=page===0;$("enrichNext").disabled=(d.books?.length||0)<30;
      renderControls(getState());
    }catch{if(ticket===serial)$("enrichError").textContent="Could not read the dashboard. Refresh to retry.";}
  }
  function select(name,focus=true){section=name;for(const key of ["library","adventures","enrichment"]){$(key+"SubTab").setAttribute("aria-selected",String(key===name));$(key+"SubTab").tabIndex=key===name?0:-1;$(key+"SubPanel").hidden=key!==name;}books.activate(active&&name==="library");adventures?.activate(active&&name==="adventures");if(focus)$(name+"SubTab").focus();if(name==="enrichment")void load();}
  for(const name of ["library","adventures","enrichment"]){$(name+"SubTab").onclick=()=>select(name);$(name+"SubTab").onkeydown=e=>{if(["ArrowLeft","ArrowRight","Home","End"].includes(e.key)){e.preventDefault();select(e.key==="Home"?"library":e.key==="End"?"enrichment":["library","adventures","enrichment"][( ["library","adventures","enrichment"].indexOf(section)+(e.key==="ArrowRight"?1:2))%3]);}};}
  $("enrichStart").onclick=()=>invoke(()=>api.StartEnrichment());$("enrichStop").onclick=()=>invoke(()=>api.StopEnrichment());$("enrichRefresh").onclick=()=>{void load();};
  $("enrichPrevious").onclick=()=>{if(page){page--;void load();}};$("enrichNext").onclick=()=>{page++;void load();};
  function renderControls(s){
    const m=s.maintenance||{},r=s.enrichment||{},blocked=s.busy||s.run||s.running||m.running||m.held;
    $("enrichStart").disabled=!ready()||blocked||r.running||!s.openAI?.planEnabled||s.openAI?.running||pending===0;
    $("enrichStop").hidden=!r.running;$("enrichStop").disabled=r.state==="stopping"||s.closing;$("enrichRefresh").disabled=!ready();
    $("enrichGate").textContent=m.needsSchema?"Apply the database update in Data maintenance before enriching.":m.catalog?.pending?"Some responses still need indexing. Build D&D browsing index in Data maintenance to include all adventures.":!s.openAI?.planEnabled?"Connect OpenAI in the sidebar to enable enrichment.":blocked?"Finish the current crawl or maintenance first.":pending===0?"All indexed adventures are enriched, or no adventures are indexed yet. Unknown values count as completed.":"Runs all missing enrichments across both storefronts, independently of Library and Adventures filters.";
  }
  return {activate(value){active=value;select(section,false);},render(s){
    if(folder!==s.dataFolder){folder=s.dataFolder;page=0;serial++;pending=undefined;$("enrichResults").replaceChildren();for(const id of ["Eligible","Players","Levels","Settings","Locations","Dates","Pending"])$("enrich"+id).textContent="—";}
    renderControls(s);
    const r=s.enrichment||{};
    const waiting=(r.active||[]).filter(b=>b.stage==="waiting").length;
    const displayState=r.state==="running"&&waiting>0&&waiting===r.active.length?"waiting":r.state;
    $("enrichState").textContent=({preparing:"Preparing",running:"Enriching adventures",waiting:"Waiting for OpenAI",storefront:"Reading storefront information",web:"Looking up missing information",stopping:"Stopping",stopped:"Stopped",completed:"Pass complete",blocked:"Needs attention"})[displayState]||"Ready";
    $("enrichCurrent").textContent=r.running?`${r.active?.length||0} books in progress${waiting?` · ${waiting} waiting for OpenAI`:""}`:"";
    $("enrichActive").replaceChildren(...(r.active||[]).map(b=>{const li=node("li","");li.append(node("strong",b.name),node("span",`${b.source==="dmsguild"?"DM’s Guild":"DriveThruRPG"} · ${{web:"Looking up missing information",waiting:"Waiting for OpenAI"}[b.stage]||"Reading storefront information"}`),node("p",b.message));return li;}));$("enrichMessage").textContent=r.message||"Enrich fills missing player counts, character levels, settings, locations and in-world dates. Unknown values count as enriched after a successful lookup.";
    $("enrichProgress").max=Math.max(1,r.total||0);$("enrichProgress").value=r.processed||0;
    $("enrichRunCounts").textContent=r.state?`${r.processed||0} / ${r.total||0} books checked · ${r.enriched||0} enriched (${r.unknown||0} with unknown values) · ${r.failed||0} issues · ${r.webPasses||0} web fallback passes`:"";
    const key=JSON.stringify([s.dataFolder,s.loading,s.maintenance?.needsSchema,s.maintenance?.running,s.running,r.running,r.processed]);if(signature!==key){signature=key;void load();}
  }};
}
