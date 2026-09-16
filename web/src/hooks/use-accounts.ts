import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import { toast } from "sonner";

export interface OVHAccount {
  id: string;
  name: string;
  endpoint: string;
  zone: string;
  appKey: string;
  appSecret: string;
  consumerKey: string;
  iam: string;
  /** 脱敏后的账户 OVH API 专用代理；空=明确直连 */
  proxyUrl: string;
  expectedOutboundIp: string;
  actualOutboundIp: string;
  outboundIpStatus: "pending" | "verified" | "mismatch" | "failed" | "";
  outboundIpCheckedAt: string;
  outboundIpError: string;
  isDefault: boolean;
  createdAt: string;

  // ── 出站配置 ──
  // proxyUrl 是**打过码的**(密码换成 ***,主机端口保留)。只能用来显示,
  // 绝不能当成真值再提交回去 —— 那会把 "***" 当成密码存进库,代理从此连不上,
  // 而后端配了代理就不会退回直连,表现是这个账户在补货那一刻一单都下不出去。
  // (声明见上方 proxyUrl —— 这里只是把这层"打码"语义讲清楚)
}

export interface AccountInput {
  name: string;
  zone: string;
  endpoint?: string; // 可空, 后端按 zone 推
  appKey: string;
  appSecret: string;
  consumerKey: string;
  iam?: string;
  expectedOutboundIp?: string;
  useDirect?: boolean;
  setDefault?: boolean;

  // 代理是**指针语义**,跟上面几个字段的"空 = 保持原值"不一样:
  //   字段不出现(undefined) = 不改
  //   ""                    = 清掉(改回直连)
  //   非空                  = 设成这个
  // 所以「输入框留空」必须翻译成"不传这个 key",不能翻译成空串 ——
  // 空串会把用户配好的代理悄悄清掉,而清掉代理的表现是一切正常、隔离却没了。
  // 反过来,清代理也只有发空串这一条路,界面上必须给一个明确的清除动作。
  // (注意别发 null:Go 那边 *string 收到 null 也是 nil,等于"不改"。)
  proxyUrl?: string;
}

const ACCOUNTS_KEY = ["accounts", "list"] as const;

/**
 * 创建 / 更新 / 验证账户时后端带回来的子公司错配说明(空 = 没问题)。
 *
 * 后端(handlers.SubsidiaryMismatchNote)拿账户里存的 zone 跟 OVH /me 返回的 ovhSubsidiary 比:
 * zone 决定目录站点、价格币种和下单 region,ovhSubsidiary 才是 OVH 认的归属。
 * 两者不同区时凭据依然 valid=true,所有请求却都会打到错误的站点 —— 这段话是用户在
 * 真正下单失败之前唯一能看到的信号,必须原样显示,不能只吞成一句"验证通过"。
 */
export interface AccountVerifyResult {
  valid: boolean;
  subsidiaryWarning?: string;
}

export interface AccountProxyCheckInput {
  accountId?: string;
  proxyUrl: string;
  expectedOutboundIp: string;
}

export interface AccountProxyCheckResult {
  healthy: boolean;
  proxy: string;
  latencyMs?: number;
  expectedOutboundIp: string;
  actualOutboundIp: string;
  outboundIpStatus: string;
  outboundIpError: string;
  outboundIpCheckedAt: string;
}

/**
 * 全部账户列表(默认账户排首位)。
 *
 * refetchInterval 目前只有设置页用:后端每 30 秒对代理账户强制复检一轮出口 IP,
 * 设置页跟着刷,卡片上的出口 IP / 阻断状态才不会是过去时。
 */
export function useAccounts(opts?: { refetchInterval?: number }) {
  return useQuery({
    queryKey: ACCOUNTS_KEY,
    queryFn: async () => {
      const res = await api.get<{ accounts: OVHAccount[] }>("/accounts");
      return res.data.accounts || [];
    },
    staleTime: 5 * 60_000,
    refetchInterval: opts?.refetchInterval,
  });
}

/** 默认账户(取列表中 isDefault, 没有就第一个) */
export function useDefaultAccount(): OVHAccount | null {
  const q = useAccounts();
  const list = q.data || [];
  return list.find((a) => a.isDefault) || list[0] || null;
}

/** 创建账户。后端会自动调 /me 验证,返回 valid 字段 */
export function useCreateAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: AccountInput) => {
      const res = await api.post<{ account: OVHAccount } & AccountVerifyResult>("/accounts", input);
      return res.data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      if (data.valid) {
        toast.success(`账户 ${data.account.name} 创建成功`);
      } else {
        toast.warning(`账户已保存,但 OVH 验证失败,请检查凭据`);
      }
      // 子公司填错不会让 valid 变 false,但会让目录/价格/下单全部走错站点,单独长时间提示
      if (data.subsidiaryWarning) {
        toast.warning(data.subsidiaryWarning, { duration: 15000 });
      }
    },
    onError: (e: any) => toast.error(e?.response?.data?.error || "创建失败"),
  });
}

/** 更新账户 */
export function useUpdateAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }: { id: string; input: Partial<AccountInput> }) => {
      const res = await api.put<{ account: OVHAccount } & AccountVerifyResult>(`/accounts/${id}`, input);
      return res.data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      toast.success("账户已更新");
      if (!data.valid) {
        toast.warning("账户已保存,但 OVH 验证失败,请检查凭据");
      }
      if (data.subsidiaryWarning) {
        toast.warning(data.subsidiaryWarning, { duration: 15000 });
      }
    },
    onError: (e: any) => toast.error(e?.response?.data?.error || "更新失败"),
  });
}

/**
 * 使用当前表单代理与预期 IP 检测出口；不保存表单内容，也不发送 OVH 鉴权请求。
 * 传了 accountId 且表单里代理留空时,后端回落到该账户已保存的代理与预期 IP ——
 * 「测试出口 IP」按钮因此既能测未保存的表单值,也能测已保存的配置。
 */
export function useCheckAccountProxy() {
  return useMutation({
    mutationFn: async (input: AccountProxyCheckInput) => {
      try {
        return (await api.post<AccountProxyCheckResult>("/accounts/proxy-check", input)).data;
      } catch (e: any) {
        const data = e?.response?.data as Partial<AccountProxyCheckResult> | undefined;
        if (data?.outboundIpStatus) return data as AccountProxyCheckResult;
        throw e;
      }
    },
    onSuccess: (data) => {
      if (data.healthy) toast.success(`账号代理可用${data.latencyMs != null ? ` · ${data.latencyMs}ms` : ""}`);
      else toast.error(data.outboundIpError || "账号代理检测失败");
    },
    onError: (e: any) => toast.error(e?.response?.data?.error || "账号代理检测失败"),
  });
}

/** 删除账户(级联删除关联的 queue/history/sniper 记录) */
export function useDeleteAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => (await api.delete(`/accounts/${id}`)).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      // 关联数据也变了,顺手 invalidate
      qc.invalidateQueries({ queryKey: ["queue"] });
      qc.invalidateQueries({ queryKey: ["history"] });
      toast.success("账户已删除,关联数据一并清理");
    },
    onError: (e: any) => toast.error(e?.response?.data?.error || "删除失败"),
  });
}

/** 把指定账户标为默认 */
export function useSetDefaultAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => (await api.post(`/accounts/${id}/set-default`)).data,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      toast.success("已设为默认账户");
    },
    onError: (e: any) => toast.error(e?.response?.data?.error || "设默认失败"),
  });
}

/** 重新验证账户凭据(调 OVH /me) */
export function useVerifyAccount() {
  return useMutation({
    mutationFn: async (id: string) =>
      (await api.post<AccountVerifyResult>(`/accounts/${id}/verify`)).data,
    onSuccess: (data) => {
      if (data.valid) {
        toast.success("OVH 凭据验证通过");
      } else {
        toast.error("OVH 凭据验证失败,检查 AppKey / AppSecret / ConsumerKey");
      }
      // 凭据有效 ≠ 区配对了。这条警告比"验证通过"重要得多,单独弹且停久一点
      if (data.subsidiaryWarning) {
        toast.warning(data.subsidiaryWarning, { duration: 15000 });
      }
    },
  });
}

/** 按 ID 查账户(从 useAccounts 缓存里找,不发请求) */
export function findAccountByID(accounts: OVHAccount[] | undefined, id: string): OVHAccount | undefined {
  if (!accounts || !id) return undefined;
  return accounts.find((a) => a.id === id);
}

/** zone 颜色映射, 用于"区域"徽章(设置页的大区标识; EU 蓝 / US 红 / CA 绿 等) */
export function accountChipColor(zone: string): string {
  const z = zone.toUpperCase();
  if (z === "US") return "bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300";
  if (z === "CA" || z === "QC") return "bg-green-100 text-green-700 dark:bg-green-950/40 dark:text-green-300";
  if (z === "ASIA" || z === "SG" || z === "AU" || z === "IN") return "bg-orange-100 text-orange-700 dark:bg-orange-950/40 dark:text-orange-300";
  // EU 系
  return "bg-blue-100 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300";
}

/**
 * 账户徽章的调色板。
 *
 * 以前账户 chip 也按 zone 上色,同一个大区下开两个账户就完全同色 ——
 * 抢购历史里一行行往下翻时分不清哪一单是哪个号下的。改成按账户 id 稳定取色:
 * 同一个账户永远同色,不同账户(哪怕同区)相互区分。
 */
const ACCOUNT_CHIP_PALETTE = [
  "bg-blue-100 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300",
  "bg-green-100 text-green-700 dark:bg-green-950/40 dark:text-green-300",
  "bg-orange-100 text-orange-700 dark:bg-orange-950/40 dark:text-orange-300",
  "bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300",
  "bg-purple-100 text-purple-700 dark:bg-purple-950/40 dark:text-purple-300",
  "bg-cyan-100 text-cyan-700 dark:bg-cyan-950/40 dark:text-cyan-300",
  "bg-pink-100 text-pink-700 dark:bg-pink-950/40 dark:text-pink-300",
  "bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300",
  "bg-teal-100 text-teal-700 dark:bg-teal-950/40 dark:text-teal-300",
  "bg-indigo-100 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300",
];

/** 按账户 id 取稳定色:同一个账户永远同色,不同账户相互区分 */
export function accountChipColorForId(id: string): string {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) | 0;
  return ACCOUNT_CHIP_PALETTE[Math.abs(h) % ACCOUNT_CHIP_PALETTE.length];
}
