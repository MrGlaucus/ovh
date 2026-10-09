import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/query";
import type { PurchaseHistory } from "./use-history";

export interface OfficialOrder {
  orderId: string;
  date: string;
  expirationDate?: string;
  retractionDate?: string;
  pdfUrl?: string;
  url?: string;
  priceWithTax?: { value: number; text: string; currencyCode: string };
  orderStatus?: string;
  orderStatusAt?: string;
  statusError?: string;
  refund?: PurchaseHistory["refund"];
  refundCheckedAt?: string;
  refundCheckError?: string;
  refundSkipped?: boolean;
  histories: { id: string; planCode: string; datacenter: string }[];
}
interface OrdersSnapshot {
  accountId: string;
  items: OfficialOrder[];
  updatedAt?: string;
  status: "idle" | "running" | "completed" | "failed";
  error?: string;
  failed: number;
  warning?: string;
}
const ordersKey = (account: string, order: string) => ["account", "orders", account, order] as const;

export function useAccountOrders(account: string, order = "", enabled = true) {
  const qc = useQueryClient();
  const observed = useRef("");
  const query = useQuery({
    queryKey: ordersKey(account, order),
    queryFn: async ({ signal }) => (await api.get<OrdersSnapshot>("/ovh/account/orders", { params: { account, order }, signal })).data,
    enabled,
    staleTime: 0,
    refetchInterval: (q) => q.state.data?.status === "running" ? 2000 : false,
  });
  useEffect(() => {
    const d = query.data;
    if (d?.status === "completed" && d.updatedAt && observed.current !== `${d.accountId}:${d.updatedAt}`) {
      observed.current = `${d.accountId}:${d.updatedAt}`;
      void qc.invalidateQueries({ queryKey: qk.history() });
    }
  }, [query.data, qc]);
  const refresh = useMutation({
    mutationFn: async (params: { account: string; order: string }) =>
      (await api.post<OrdersSnapshot>("/ovh/account/orders/refresh", null, { params })).data,
    onSuccess: (data, params) => qc.setQueryData(ordersKey(params.account, params.order), data),
    onError: (_error, params) => { void qc.invalidateQueries({ queryKey: ordersKey(params.account, params.order) }); },
  });
  return { query, refresh, running: refresh.isPending || query.data?.status === "running" };
}
