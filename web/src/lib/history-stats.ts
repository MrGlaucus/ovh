import type { PurchaseHistory } from "@/hooks/use-history";

export type HistoryRange = "today" | "month" | "all";
export const statisticStatuses = ["notPaid", "checking", "delivering", "delivered", "cancelling", "cancelled", "documentsRequested", "unknown", "unqueried"] as const;
export interface HistoryStats {
  accountId: string;
  orders: number;
  statuses: Record<string, number>;
  refunded: number;
  money: Record<string, { paid: number; refund: number }>;
  missingPaid: number;
  missingRefund: number;
  unconfirmed: number;
}
export function storedTime(value: string) {
  return Date.parse(/(?:Z|[+-]\d{2}:?\d{2})$/i.test(value) ? value : `${value}Z`);
}
export function rangeStart(range: HistoryRange, now: number) {
  if (range === "all") return -Infinity;
  const bj = new Date(now + 8 * 3600000);
  return Date.UTC(bj.getUTCFullYear(), bj.getUTCMonth(), range === "today" ? bj.getUTCDate() : 1) - 8 * 3600000;
}
function empty(accountId: string): HistoryStats {
  return { accountId, orders: 0, statuses: {}, refunded: 0, money: {}, missingPaid: 0, missingRefund: 0, unconfirmed: 0 };
}

/** Only panel history, grouped by account + original order. All money is in minor units. */
export function historyStats(items: PurchaseHistory[], range: HistoryRange, now: number, account: string) {
  const orders = new Map<string, PurchaseHistory[]>();
  for (const item of items) {
    if (item.status !== "success" || !item.orderId) continue;
    if (account !== "all" && (account === "unassigned" ? !!item.accountId : item.accountId !== account)) continue;
    const key = JSON.stringify([item.accountId, item.orderId]);
    orders.set(key, [...(orders.get(key) || []), item]);
  }
  const total = empty("all");
  const accounts = new Map<string, HistoryStats>();
  const refundIDs = new Set<string>();
  const start = rangeStart(range, now);
  let invalidDates = 0;
  for (const rows of orders.values()) {
    const dates = rows.map((r) => storedTime(r.purchaseTime)).filter(Number.isFinite);
    const date = Math.min(...dates);
    if (!dates.length) { invalidDates++; if (range !== "all") continue; }
    if (dates.length && (date < start || date > now)) continue;
    // Most recently checked status wins; missing timestamps fall back to purchase time.
    rows.sort((a, b) => (storedTime(b.orderStatusAt || b.purchaseTime) || 0) - (storedTime(a.orderStatusAt || a.purchaseTime) || 0));
    const item = rows[0];
    let group = accounts.get(item.accountId);
    if (!group) { group = empty(item.accountId); accounts.set(item.accountId, group); }
    const targets = [total, group];
    const status = statisticStatuses.some((s) => s === item.orderStatus) ? item.orderStatus! : "unqueried";
    const refunds = rows.flatMap((r) => r.refund?.id ? (r.refund.records?.length ? r.refund.records : [r.refund]) : []);
    const paid = rows.some((r) => r.paymentConfirmed || r.orderStatus === "delivering" || r.orderStatus === "delivered") || refunds.length > 0;
    for (const target of targets) {
      target.orders++;
      target.statuses[status] = (target.statuses[status] || 0) + 1;
      if (refunds.length) target.refunded++;
      if (!paid && status !== "notPaid" && status !== "checking") target.unconfirmed++;
    }
    const addMoney = (price: PurchaseHistory["price"], kind: "paid" | "refund") => {
      const amount = price?.withTax;
      const currency = price?.currencyCode?.trim().toUpperCase();
      for (const target of targets) {
        if (typeof amount !== "number" || !Number.isFinite(amount) || !currency || (kind === "paid" && amount < 0)) {
          if (kind === "paid") target.missingPaid++; else target.missingRefund++;
          continue;
        }
        const money = target.money[currency] ??= { paid: 0, refund: 0 };
        money[kind] += Math.round(Math.abs(amount) * 100);
      }
    };
    if (paid) addMoney(rows.find((r) => typeof r.price?.withTax === "number" && r.price?.currencyCode)?.price, "paid");
    for (const refund of refunds) {
      const key = JSON.stringify([item.accountId, refund.id]);
      if (refundIDs.has(key)) continue;
      refundIDs.add(key);
      addMoney(refund.price, "refund");
    }
  }
  return { total, accounts: [...accounts.values()], invalidDates };
}
