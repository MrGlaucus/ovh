import ts from "typescript";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../src/lib/restock-heatmap.ts", import.meta.url), "utf8");
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { restockHeatmap, stockTime, stockConfigKey } = await import(`data:text/javascript;base64,${Buffer.from(js).toString("base64")}`);
const entry = (overrides = {}) => ({ timestamp: "2026-10-11T16:00:00Z", datacenter: "gra", changeType: "available", config: { config_key: "ks.ram32.disk1", display: "32G" }, ...overrides });
assert.equal(stockTime("2026-10-12 00:00:00"), Date.parse("2026-10-11T16:00:00Z"));
const records = [entry(), entry(), entry({ timestamp: "2026-10-12T00:00:00+08:00" }), entry({ datacenter: "rbx" }), entry({ config: { config_key: "ks.ram64.disk1", display: "64G" } }), entry({ changeType: "unavailable" }), entry({ changeType: "price_check_failed" }), entry({ timestamp: "invalid" }), entry({ timestamp: "2026-10-11T15:59:00Z" })];
const data = restockHeatmap(records);
assert.equal(data.total, 4);
assert.equal(data.grid[0][0], 3); // Monday midnight Beijing, independent of browser timezone
assert.equal(data.grid[6][23], 1);
assert.equal(data.invalid, 1);
assert.equal(data.max, 3);
assert.equal(restockHeatmap(records.filter((e) => e.datacenter === "rbx")).total, 1);
assert.equal(restockHeatmap(records.filter((e) => stockConfigKey(e) === "key:ks.ram64.disk1")).total, 1);
assert.equal(restockHeatmap([]).total, 0);
assert.notEqual(stockConfigKey(entry({ config: { memory: "32G", storage: "SSD" } })), stockConfigKey(entry({ config: { memory: "64G", storage: "SSD" } })));
assert.notEqual(stockConfigKey(entry()), stockConfigKey(entry({ config: { config_key: "different", display: "32G" } })));
console.log("restock heatmap tests passed");
