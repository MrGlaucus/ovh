import ts from "typescript";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../src/lib/history-stats.ts", import.meta.url), "utf8");
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { historyStats, rangeStart } = await import(`data:text/javascript;base64,${Buffer.from(js).toString("base64")}`);
const now = Date.parse("2026-10-14T12:00:00+08:00");
assert.equal(rangeStart("today", now), Date.parse("2026-10-13T16:00:00Z"));
assert.equal(rangeStart("month", now), Date.parse("2026-09-30T16:00:00Z"));
const order = (id, overrides = {}) => ({ id, orderId: id, accountId: "a", status: "success", purchaseTime: "2026-10-13T16:00:00Z", orderStatus: "delivering", price: { withTax: 10.1, currencyCode: "EUR" }, ...overrides });
const rows = [
  order("1"), order("1", { id: "duplicate" }),
  order("2", { orderStatus: "checking" }),
  order("3", { accountId: "b", price: { withTax: 20, currencyCode: "USD" } }),
  order("4", { orderStatus: "cancelled", paymentConfirmed: true, refund: { id: "r", records: [{ id: "r", price: { withTax: -3, currencyCode: "EUR" } }, { id: "r2", price: { withTax: 2, currencyCode: "EUR" } }] } }),
  order("5", { purchaseTime: "2026-09-20T00:00:00Z", refund: { id: "old-refund", date: "2026-10-14", price: { withTax: 1, currencyCode: "EUR" } } }),
  order("6", { price: { withTax: 99 } }),
  order("7", { status: "failed" }),
  order("8", { orderStatus: "notPaid" }),
  order("9", { purchaseTime: "2026-10-13T15:59:59Z" }),
  order("10", { purchaseTime: "2026-10-13T16:00:00" }), // legacy UTC
];
let stats = historyStats(rows, "today", now, "all");
assert.equal(stats.total.orders, 7);
assert.deepEqual(stats.total.money, { EUR: { paid: 3030, refund: 500 }, USD: { paid: 2000, refund: 0 } });
assert.equal(stats.total.missingPaid, 1);
assert.equal(stats.total.refunded, 1);
assert.equal(historyStats(rows, "today", now, "a").total.orders, 6);
assert.equal(historyStats(rows, "all", now, "all").total.money.EUR.refund, 600);
assert.equal(historyStats(rows, "month", now, "all").total.orders, 8);
assert.equal(historyStats([order("1", { accountId: "" })], "all", now, "unassigned").total.orders, 1);
assert.equal(historyStats([order("1", { orderStatus: "cancelled" })], "all", now, "all").total.unconfirmed, 1);
assert.equal(historyStats([order("1", { purchaseTime: "bad" })], "today", now, "all").total.orders, 0);
console.log("history statistics tests passed");
