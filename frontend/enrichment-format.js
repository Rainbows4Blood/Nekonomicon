export const contextFields = [["settings","Settings"],["locations","Locations"],["dates","In-world dates"]];
export function contextFacts(result={}, completed={}) {
  return contextFields.map(([key,label])=>{
    const r=result?.[key]||{};
    let values=[];
    if(key==="settings") values=[...(r.names||[]),...(r.agnostic?["Agnostic (any campaign setting)"]:[])];
    else values=(r.items||[]).map(item=>{
      const suffix=(key==="dates"?[item.calendar,item.setting]:[item.setting]).filter(Boolean).join(" · ");
      return `${key==="dates"?item.date:item.name}${suffix?` (${suffix})`:""}`;
    });
    return {key,label,value:values.length?values.join("; "):completed[key]?"Unknown (enriched)":"Not enriched yet",evidence:r.evidence||"",sources:r.sources||[]};
  });
}
