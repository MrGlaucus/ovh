import { useQuery } from "@tanstack/react-query";
import axios from "axios";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { OVH_SUBSIDIARIES } from "@/lib/ovh-subsidiaries";

export interface DatacenterInfo {
  datacenter: string;
  availability: string;
}

export interface AvailabilityItem {
  fqn: string;
  memory: string;
  planCode: string;
  server: string;
  storage: string;
  systemStorage?: string;
  datacenters: DatacenterInfo[];
}

/** 从后端 config 读取 endpoint 决定调哪个 OVH 公开 API */
async function getApiBaseUrl(): Promise<string> {
  const res = await api.get("/settings");
  const endpoint = res.data?.endpoint || "ovh-eu";
  switch (endpoint) {
    case "ovh-us":
      return "https://api.us.ovhcloud.com";
    case "ovh-ca":
      return "https://ca.api.ovh.com";
    default:
      return "https://eu.api.ovh.com";
  }
}

/** 同一 endpoint 读出来 + 默认 subsidiary（catalog 接口需要传 ovhSubsidiary 参数） */
async function getRegionInfo(): Promise<{ baseUrl: string; subsidiary: string }> {
  const res = await api.get("/settings");
  const endpoint = res.data?.endpoint || "ovh-eu";
  // 用户可能在 settings 里显式配置了 subsidiary（如 FR / DE / UK / PL / ASIA），优先用
  const explicit = (res.data?.subsidiary as string | undefined)?.toUpperCase();
  switch (endpoint) {
    case "ovh-us":
      return { baseUrl: "https://api.us.ovhcloud.com", subsidiary: explicit || "US" };
    case "ovh-ca":
      // ovh-ca 站点同时服务加拿大 + 亚太：默认 CA（加元），亚太用户可在设置里改 ASIA
      return { baseUrl: "https://ca.api.ovh.com", subsidiary: explicit || "CA" };
    default:
      // ovh-eu 站点下属多个子公司（IE / FR / DE / UK / PL / IT / ES…），默认 IE 用 EUR 无 VAT
      return { baseUrl: "https://eu.api.ovh.com", subsidiary: explicit || "IE" };
  }
}

/** 直接查询 OVH 公开 API 的实时可用性 */
export function useAvailability() {
  return useQuery({
    queryKey: qk.availability.all("auto"),
    queryFn: async () => {
      const baseUrl = await getApiBaseUrl();
      const res = await axios.get<AvailabilityItem[]>(
        `${baseUrl}/v1/dedicated/server/datacenter/availabilities`,
        { timeout: 30000 }
      );
      return res.data;
    },
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

/**
 * 把 OVH availabilities 数组聚合成 `{ [planCode]: { [dcCode]: status } }` 的查表。
 * 同一 planCode 下可能有多个 FQN 变体（不同 memory / storage），同 DC 取"最好的"那个：
 * 任一变体可用 → 标可用；都不可用 → unavailable；都缺 → unknown。
 */
export function buildAvailabilityMap(
  items: AvailabilityItem[] | undefined
): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {};
  if (!items) return out;
  for (const item of items) {
    const pc = item.planCode;
    if (!pc) continue;
    if (!out[pc]) out[pc] = {};
    for (const dc of item.datacenters || []) {
      const code = dc.datacenter?.toLowerCase();
      if (!code) continue;
      const existing = out[pc][code];
      const incoming = dc.availability;
      // 已经标记为可用就不被后续覆盖；否则用最新值
      const isAvail = (v: string | undefined) => !!v && v !== "unavailable" && v !== "unknown";
      if (isAvail(existing)) continue;
      out[pc][code] = incoming;
    }
  }
  return out;
}

// ─────────────────────────────── 价格计算（对齐 ovhjk/parser/price.go） ───────────────────────────────

export interface CatalogPricing {
  phase: number;
  description: string;
  interval: number;
  intervalUnit: string;
  price: number; // 微欧元（÷ 1e8 得欧元）
  tax: number; // 微欧元
  mode: string;
  capacities?: string[];
}

export interface CatalogAddonFamily {
  name: string;
  addons: string[];
  default?: string;
}

export interface CatalogPlan {
  planCode: string;
  invoiceName: string;
  product: string;
  pricings: CatalogPricing[];
  addonFamilies?: CatalogAddonFamily[];
}

export interface CatalogData {
  catalogId: number;
  locale: { currencyCode: string; subsidiary: string; taxRate: number };
  plans: CatalogPlan[];
  addons: CatalogPlan[];
}

export interface PriceInfo {
  /** 月费不含税（欧元 / 当地货币） */
  price: number;
  /** 月费税费 */
  tax: number;
  /** 月费含税 */
  total: number;
  /** 一次性安装费不含税 */
  installPrice: number;
  /** 安装费税费 */
  installTax: number;
  /** 货币代码 EUR / USD / CAD 等 */
  currency: string;
}

/**
 * 拉取 OVH 公共目录（每个 subsidiary 各自一份：不同币、不同税、不同促销价）。
 * - subsidiary 不传时：由后端 settings.endpoint 推断默认（EU→IE / US→US / CA→CA）
 * - subsidiary 显式传入时（用户切了地区选择器）：用指定的子公司
 * - baseUrl 也跟 subsidiary 所在站点联动：IE/FR/… → eu.api.ovh.com；US → api.us.ovhcloud.com；CA/ASIA/AU/SG/IN → ca.api.ovh.com
 */
export function useOvhCatalog(subsidiary?: string) {
  return useQuery({
    queryKey: ["ovh-catalog", "eco", subsidiary || "auto"] as const,
    queryFn: async () => {
      let baseUrl: string;
      let sub: string;
      if (subsidiary) {
        // 显式指定：根据 subsidiary 反查所属站点
        const meta = OVH_SUBSIDIARIES.find((s) => s.code === subsidiary);
        sub = subsidiary;
        if (meta?.endpoint === "ovh-us") baseUrl = "https://api.us.ovhcloud.com";
        else if (meta?.endpoint === "ovh-ca") baseUrl = "https://ca.api.ovh.com";
        else baseUrl = "https://eu.api.ovh.com";
      } else {
        const r = await getRegionInfo();
        baseUrl = r.baseUrl;
        sub = r.subsidiary;
      }
      const res = await axios.get<CatalogData>(
        `${baseUrl}/v1/order/catalog/public/eco?ovhSubsidiary=${encodeURIComponent(sub)}`,
        { timeout: 30000 }
      );
      return res.data;
    },
    staleTime: 30 * 60_000,
    gcTime: 24 * 60 * 60_000,
  });
}

/** 给定 catalog 构建按 planCode 索引的查表 + addon 查表 */
export interface CatalogIndex {
  planByCode: Record<string, CatalogPlan>;
  addonByCode: Record<string, CatalogPlan>;
  currency: string;
}
export function buildCatalogIndex(catalog: CatalogData | undefined): CatalogIndex {
  if (!catalog) return { planByCode: {}, addonByCode: {}, currency: "EUR" };
  const planByCode: Record<string, CatalogPlan> = {};
  for (const p of catalog.plans || []) planByCode[p.planCode] = p;
  const addonByCode: Record<string, CatalogPlan> = {};
  for (const a of catalog.addons || []) addonByCode[a.planCode] = a;
  return { planByCode, addonByCode, currency: catalog.locale?.currencyCode || "EUR" };
}

/** 月费：取 intervalUnit=month, interval=1, mode=default 的那条 */
function monthlyPrice(pricings: CatalogPricing[] | undefined): { price: number; tax: number; ok: boolean } {
  if (!pricings) return { price: 0, tax: 0, ok: false };
  for (const pr of pricings) {
    if (pr.intervalUnit === "month" && pr.interval === 1 && pr.mode === "default") {
      return { price: pr.price / 1e8, tax: pr.tax / 1e8, ok: true };
    }
  }
  return { price: 0, tax: 0, ok: false };
}

/** 安装费：mode=default 且 capacities 含 'installation' */
function installationPrice(pricings: CatalogPricing[] | undefined): { price: number; tax: number } {
  if (!pricings) return { price: 0, tax: 0 };
  for (const pr of pricings) {
    if (pr.mode !== "default") continue;
    if ((pr.capacities || []).includes("installation")) {
      return { price: pr.price / 1e8, tax: pr.tax / 1e8 };
    }
  }
  return { price: 0, tax: 0 };
}

/** 在 family.addons 里按前缀匹配 fqn 维度，返回 addon planCode（旧 ovhjk 同款逻辑） */
function matchAddonCode(addons: string[], fqnDim: string): string {
  if (!fqnDim) return "";
  return addons.find((c) => c.startsWith(fqnDim)) || "";
}

/** 对应 family 在 FQN 里的维度值 */
function fqnDimensionForFamily(item: AvailabilityItem, familyName: string): string {
  switch (familyName) {
    case "memory":
      return item.memory || "";
    case "storage":
      return item.storage || "";
    case "system-storage":
      return item.systemStorage || "";
    default:
      return "";
  }
}

/** 计算单个 AvailabilityItem 的总价（base + 各 family 匹配的 addon） */
export function computePrice(item: AvailabilityItem | undefined, idx: CatalogIndex): PriceInfo | null {
  if (!item) return null;
  const plan = idx.planByCode[item.planCode];
  if (!plan) return null;

  const base = monthlyPrice(plan.pricings);
  if (!base.ok) return null;

  let totalPrice = base.price;
  let totalTax = base.tax;
  const baseInstall = installationPrice(plan.pricings);
  let installPrice = baseInstall.price;
  let installTax = baseInstall.tax;

  for (const fam of plan.addonFamilies || []) {
    const dim = fqnDimensionForFamily(item, fam.name);
    if (!dim) continue;
    const addonCode = matchAddonCode(fam.addons || [], dim);
    if (!addonCode) continue;
    const addon = idx.addonByCode[addonCode];
    if (!addon) continue;
    const ap = monthlyPrice(addon.pricings);
    if (ap.ok) {
      totalPrice += ap.price;
      totalTax += ap.tax;
    }
    const ai = installationPrice(addon.pricings);
    installPrice += ai.price;
    installTax += ai.tax;
  }

  return {
    price: totalPrice,
    tax: totalTax,
    total: totalPrice + totalTax,
    installPrice,
    installTax,
    currency: idx.currency,
  };
}

/**
 * 给一组 availability items + catalog，按 planCode 算出代表价（同 planCode 多变体时
 * 取第一个能算出的 item 的价格）。返回 `{ [planCode]: PriceInfo }`。
 */
export function buildPriceMap(
  items: AvailabilityItem[] | undefined,
  idx: CatalogIndex
): Record<string, PriceInfo> {
  const out: Record<string, PriceInfo> = {};
  if (!items) return out;
  for (const item of items) {
    if (out[item.planCode]) continue;
    const p = computePrice(item, idx);
    if (p) out[item.planCode] = p;
  }
  return out;
}

/**
 * 用用户选中的 addon planCode 列表直接算价：
 * 总价 = 基础 plan 月费 + 各 addon 月费（每个 addon 自带定价，按 planCode 查 catalog）
 *
 * 与 `computePrice` 的区别：后者用 FQN 维度前缀匹配 addon；这里调用方已经知道每个组挑了哪个 addonCode，
 * 直接累加更准确（用户切换内存 / 存储等组时实时反映）。
 */
export function computePriceFromOptions(
  planCode: string,
  selectedAddonCodes: string[],
  idx: CatalogIndex
): PriceInfo | null {
  const plan = idx.planByCode[planCode];
  if (!plan) return null;
  const base = monthlyPrice(plan.pricings);
  if (!base.ok) return null;
  let totalPrice = base.price;
  let totalTax = base.tax;
  const baseInstall = installationPrice(plan.pricings);
  let installPrice = baseInstall.price;
  let installTax = baseInstall.tax;
  for (const code of selectedAddonCodes) {
    if (!code) continue;
    const addon = idx.addonByCode[code];
    if (!addon) continue;
    const ap = monthlyPrice(addon.pricings);
    if (ap.ok) {
      totalPrice += ap.price;
      totalTax += ap.tax;
    }
    const ai = installationPrice(addon.pricings);
    installPrice += ai.price;
    installTax += ai.tax;
  }
  return {
    price: totalPrice,
    tax: totalTax,
    total: totalPrice + totalTax,
    installPrice,
    installTax,
    currency: idx.currency,
  };
}

/** 友好显示：€42.99/月 含税 €51.59/月 */
export function formatPrice(p: PriceInfo | undefined | null): string {
  if (!p) return "—";
  const sym = p.currency === "EUR" ? "€" : p.currency === "USD" ? "$" : p.currency === "CAD" ? "CA$" : p.currency + " ";
  return `${sym}${p.price.toFixed(2)} / 月`;
}
