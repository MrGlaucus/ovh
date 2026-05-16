import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";

export interface AccountInfo {
  customerCode: string;
  nichandle: string;
  email: string;
  firstname?: string;
  name?: string;
  city?: string;
  country?: string;
  kycValidated?: boolean;
  state?: string;
  currency?: { code: string; symbol: string };
  /** OVH 子公司：IE / FR / DE / US / CA / ASIA / SG / AU / IN 等。决定结算货币和价格档 */
  ovhSubsidiary?: string;
}

export interface RefundRecord {
  refundId: string;
  orderId: string;
  date: string;
  priceWithTax: { value: number; text: string; currencyCode: string };
  pdfUrl?: string;
}

export interface EmailHistoryEntry {
  id: number;
  date: string;
  subject: string;
  body: string;
}

/** OVH 账户信息（后端直接返回 OVH /me 字段） */
export function useAccountInfo() {
  return useQuery({
    queryKey: qk.account.info(),
    queryFn: async () => (await api.get<AccountInfo>("/ovh/account/info")).data,
  });
}

/** 退款记录（后端直接返回数组） */
export function useRefunds() {
  return useQuery({
    queryKey: qk.account.refunds(),
    queryFn: async () => (await api.get<RefundRecord[]>("/ovh/account/refunds")).data,
  });
}

/** 邮件历史（后端直接返回数组） */
export function useEmails() {
  return useQuery({
    queryKey: qk.account.emails(),
    queryFn: async () => (await api.get<EmailHistoryEntry[]>("/ovh/account/email-history")).data,
  });
}
