import { Globe2, MapPin } from "lucide-react";
import { OVH_DATACENTERS } from "@/lib/datacenters";

const flags: Record<string, string> = {
  法国: "🇫🇷", 加拿大: "🇨🇦", 印度: "🇮🇳", 波兰: "🇵🇱",
  德国: "🇩🇪", 英国: "🇬🇧", 美国西部: "🇺🇸", 美国东部: "🇺🇸",
  新加坡: "🇸🇬", 澳大利亚: "🇦🇺",
};

export function DatacenterChip({ code }: { code: string }) {
  const normalized = code.trim().toLowerCase();
  const dc = OVH_DATACENTERS.find((item) => item.code === normalized || item.apiCode === normalized);
  const displayCode = (dc?.code || normalized).toUpperCase();
  const label = dc ? `${dc.region} · ${dc.name}` : "未知位置";
  return (
    <span title={`${displayCode} · ${label}`} className="inline-flex max-w-full flex-wrap items-center gap-x-1.5 rounded-md border border-border bg-secondary/40 px-2 py-1 text-[11px] leading-4">
      {dc && <span aria-hidden="true">{flags[dc.region]}</span>}
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
