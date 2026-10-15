import { Globe2, MapPin } from "lucide-react";
import { OVH_DATACENTERS } from "@/lib/datacenters";
import fr from "@/assets/flags/fr.svg";
import ca from "@/assets/flags/ca.svg";
import india from "@/assets/flags/in.svg";
import pl from "@/assets/flags/pl.svg";
import de from "@/assets/flags/de.svg";
import gb from "@/assets/flags/gb.svg";
import us from "@/assets/flags/us.svg";
import sg from "@/assets/flags/sg.svg";
import au from "@/assets/flags/au.svg";

const flags: Record<string, string> = {
  法国: fr, 加拿大: ca, 印度: india, 波兰: pl,
  德国: de, 英国: gb, 美国西部: us, 美国东部: us,
  新加坡: sg, 澳大利亚: au,
};

export function DatacenterChip({ code }: { code: string }) {
  const normalized = code.trim().toLowerCase();
  const dc = OVH_DATACENTERS.find((item) => item.code === normalized || item.apiCode === normalized);
  const displayCode = (dc?.code || normalized).toUpperCase();
  const label = dc ? `${dc.region} · ${dc.name}` : "未知位置";
  return (
    <span title={`${displayCode} · ${label}`} className="inline-flex max-w-full flex-wrap items-center gap-x-1.5 rounded-md border border-border bg-secondary/40 px-2 py-1 text-[11px] leading-4">
      {dc && flags[dc.region] && <img src={flags[dc.region]} alt="" aria-hidden="true" width={16} height={12} className="h-3 w-4 shrink-0 rounded-[2px] object-cover ring-1 ring-black/10" />}
      <span className="font-mono font-semibold break-all">{displayCode || "未指定机房"}</span>
      <span className="text-muted-foreground">{label}</span>
    </span>
  );
}

/** 这里只展示订阅范围，不将选中的机房误标成当前有货。 */
export function MonitorDatacenters({ codes }: { codes: string[] }) {
  const locations = [...new Set(codes.map((raw) => {
    const code = raw.trim().toLowerCase();
    return OVH_DATACENTERS.find((dc) => dc.code === code || dc.apiCode === code)?.code || code;
  }).filter(Boolean))];

  return (
    <div className="mb-2 mt-1.5 flex flex-wrap items-center gap-1.5" aria-label="监控机房">
      <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground mr-0.5">
        <MapPin className="h-3 w-3" aria-hidden="true" />
        监控机房{locations.length > 0 ? ` · ${locations.length}` : ""}
      </span>
      {locations.length === 0 ? (
        <span className="inline-flex items-center gap-1.5 rounded-md border border-border bg-secondary/40 px-2 py-1 text-[11px]" title="监控该型号所有可用机房">
          <Globe2 className="h-3 w-3 text-muted-foreground" aria-hidden="true" />所有机房
        </span>
      ) : locations.map((code) => <DatacenterChip key={code} code={code} />)}
    </div>
  );
}
