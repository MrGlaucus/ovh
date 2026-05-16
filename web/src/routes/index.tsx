import { createFileRoute, Link } from "@tanstack/react-router";
import {
  BarChart3,
  ClipboardList,
  Server,
  CheckCircle2,
  ChevronRight,
  Plus,
  Clock,
  Activity,
  Link2,
  Bot,
  Bell,
  Info,
  CheckCheck,
  type LucideIcon,
} from "lucide-react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as RechartsTooltip,
  Line,
  LineChart,
} from "recharts";
import { PageHeader } from "@/components/common/PageHeader";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Chip } from "@/components/common/Chip";
import { StatusDot } from "@/components/common/StatusDot";
import { EmptyState } from "@/components/common/EmptyState";
import { Skeleton } from "@/components/common/Skeleton";
import { useStats, usePerfTrend, type SparkPoint } from "@/hooks/use-stats";
import { useQueueList, type QueueItem } from "@/hooks/use-queue";
import { useLogs, type LogEntry } from "@/hooks/use-logs";

/** 仪表盘：VPS 控制台总台。顶部 3 张 KPI + 中部活跃队列/系统状态 + 底部最近活动 */
export const Route = createFileRoute("/")({
  component: DashboardPage,
});

function DashboardPage() {
  const stats = useStats();
  const queue = useQueueList();
  const logs = useLogs(true);
  const perf = usePerfTrend();

  const activeQueue: QueueItem[] = (queue.data || [])
    .filter((i) => ["running", "pending", "paused"].includes(i.status))
    .slice(0, 4);

  const recentLogs: LogEntry[] = (logs.data || [])
    .slice()
    .reverse()
    .filter((l) => l.level === "INFO" || l.level === "WARNING")
    .slice(0, 10);

  return (
    <div className="space-y-6">
      <PageHeader icon={BarChart3} title="仪表盘" description="OVH 服务器抢购平台状态概览" />

      {/* 顶部 KPI */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <KpiCard
          label="活跃队列"
          value={stats.data?.activeQueues}
          icon={ClipboardList}
          linkTo="/queue"
          linkText="查看队列"
          loading={stats.isPending}
        />
        <KpiCard
          label="服务器总数"
          value={stats.data?.totalServers}
          extra={
            stats.data && (
              <span className="text-[12px] text-muted-foreground ml-2">
                可用 <span className="font-semibold text-success">{stats.data.availableServers}</span>
              </span>
            )
          }
          icon={Server}
          linkTo="/servers"
          linkText="查看服务"
          loading={stats.isPending}
        />
        <KpiCard
          label="抢购成功"
          value={stats.data?.purchaseSuccess}
          icon={CheckCircle2}
          linkTo="/history"
          linkText="查看历史"
          loading={stats.isPending}
        />
      </div>

      {/* 中部：活跃队列 + 系统状态 */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <Card className="lg:col-span-2">
          <CardContent className="p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <ClipboardList className="w-4 h-4 text-muted-foreground" />
                <h2 className="text-[15px] font-semibold">活跃队列</h2>
              </div>
              <Link to="/queue" className="text-xs text-muted-foreground hover:text-foreground inline-flex items-center gap-1">
                查看全部
                <ChevronRight className="w-3 h-3" />
              </Link>
            </div>
            {queue.isPending ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <Skeleton key={i} className="h-16 rounded-xl" />
                ))}
              </div>
            ) : activeQueue.length === 0 ? (
              <EmptyState
                icon={Calendar}
                title="暂无活跃任务"
                action={
                  <Button asChild>
                    <Link to="/queue">
                      <Plus className="w-4 h-4" />
                      创建抢购任务
                    </Link>
                  </Button>
                }
              />
            ) : (
              <div className="space-y-2">
                {activeQueue.map((q) => (
                  <div
                    key={q.id}
                    className="flex items-center justify-between gap-3 rounded-xl px-4 py-3 bg-secondary/50 border border-border"
                  >
                    <div className="min-w-0 flex-1">
                      <p className="font-medium text-sm truncate">{q.planCode}</p>
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground mt-0.5">
                        <span className="inline-flex items-center gap-1">
                          <Clock className="w-3 h-3" />
                          {q.datacenter.toUpperCase()}
                        </span>
                        <span className="text-muted-foreground/50">·</span>
                        <span>第 {q.retryCount + 1} 次尝试</span>
                      </div>
                    </div>
                    <QueueStatusChip status={q.status} />
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardContent className="p-6">
            <div className="flex items-center gap-2 mb-4">
              <CheckCheck className="w-4 h-4 text-muted-foreground" />
              <h2 className="text-[15px] font-semibold">系统状态</h2>
            </div>
            <div className="space-y-1">
              <SystemRow
                icon={<Link2 className="w-3.5 h-3.5" />}
                label="API 连接"
                ok={!!stats.data}
                onText="已连接"
                offText="未连接"
              />
              <SystemRow
                icon={<Bot className="w-3.5 h-3.5" />}
                label="自动抢购"
                ok={(stats.data?.activeQueues || 0) > 0}
                onText="运行中"
                offText="暂无任务"
                neutralOff
              />
              <SystemRow
                icon={<Bell className="w-3.5 h-3.5" />}
                label="服务器监控"
                ok={!!stats.data?.monitorRunning}
                onText="运行中"
                offText="待启用"
                warnOff
              />
              <div className="flex justify-between items-center px-3 py-2.5 mt-1 border-t border-border pt-3">
                <div className="inline-flex items-center gap-2 text-xs text-muted-foreground">
                  <Info className="w-3.5 h-3.5" />
                  系统版本
                </div>
                <span className="text-xs font-mono font-semibold">v3.0.0</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 底部：最近活动 + 性能趋势 */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <Card>
          <CardContent className="p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Clock className="w-4 h-4 text-muted-foreground" />
                <h2 className="text-[15px] font-semibold">最近活动</h2>
              </div>
              <Link
                to="/logs"
                className="text-xs text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
              >
                查看全部
                <ChevronRight className="w-3 h-3" />
              </Link>
            </div>
            {logs.isPending ? (
              <div className="space-y-3">
                {Array.from({ length: 5 }).map((_, i) => (
                  <Skeleton key={i} className="h-12 rounded-xl" />
                ))}
              </div>
            ) : recentLogs.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">暂无活动记录</p>
            ) : (
              <div className="space-y-2 max-h-[280px] overflow-y-auto pr-1">
                {recentLogs.map((log) => (
                  <ActivityRow key={log.id} log={log} />
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardContent className="p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Activity className="w-4 h-4 text-muted-foreground" />
                <h2 className="text-[15px] font-semibold">
                  抢购趋势
                </h2>
              </div>
              <Chip tone="default">近 24 小时</Chip>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-5">
              <SparkKpi label="抢购次数" value={String(perf.kpi.total)} data={perf.sparkTotal} />
              <SparkKpi label="成功" value={String(perf.kpi.success)} data={perf.sparkSuccess} />
              <SparkKpi label="失败" value={String(perf.kpi.failed)} data={perf.sparkFailed} />
              <SparkKpi
                label="成功率"
                value={perf.kpi.total > 0 ? `${perf.kpi.successRate.toFixed(1)}%` : "—"}
                data={perf.sparkRate}
              />
            </div>

            <div style={{ width: "100%", height: 200 }}>
              <ResponsiveContainer>
                <AreaChart data={perf.perfData} margin={{ top: 5, right: 10, left: -20, bottom: 0 }}>
                  <defs>
                    <linearGradient id="perfGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="hsl(var(--foreground))" stopOpacity={0.18} />
                      <stop offset="100%" stopColor="hsl(var(--foreground))" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" vertical={false} />
                  <XAxis
                    dataKey="time"
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    style={{ fontSize: 10 }}
                    interval={3}
                  />
                  <YAxis
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    style={{ fontSize: 10 }}
                    allowDecimals={false}
                  />
                  <RechartsTooltip
                    contentStyle={{
                      backgroundColor: "hsl(var(--popover))",
                      border: "1px solid hsl(var(--border))",
                      borderRadius: "8px",
                      fontSize: 12,
                      color: "hsl(var(--popover-foreground))",
                    }}
                    cursor={{ stroke: "hsl(var(--border))" }}
                    formatter={(_v: any, _name: any, item: any) => {
                      const p = item?.payload || {};
                      return [`总 ${p.value} · 成 ${p.success} · 败 ${p.failed}`, "抢购数"];
                    }}
                  />
                  <Area
                    type="monotone"
                    dataKey="value"
                    name="抢购数"
                    stroke="hsl(var(--foreground))"
                    strokeWidth={2}
                    fill="url(#perfGradient)"
                    dot={false}
                    activeDot={{ r: 4, fill: "hsl(var(--foreground))", stroke: "hsl(var(--background))", strokeWidth: 2 }}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

/** 小型 KPI + 迷你折线，用于性能趋势卡的顶部四联指标 */
function SparkKpi({ label, value, data }: { label: string; value: string; data: SparkPoint[] }) {
  return (
    <div className="rounded-xl border border-border bg-secondary/40 px-3 py-2.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[11px] text-muted-foreground">{label}</span>
        <span className="text-sm font-semibold tabular-nums">{value}</span>
      </div>
      <div className="mt-1 h-7">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 2, right: 0, left: 0, bottom: 0 }}>
            <Line
              type="monotone"
              dataKey="v"
              stroke="hsl(var(--foreground))"
              strokeWidth={1.5}
              dot={false}
              isAnimationActive={false}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

/* ---------- 小组件 ---------- */

function KpiCard({
  label,
  value,
  extra,
  icon: Icon,
  linkTo,
  linkText,
  loading,
}: {
  label: string;
  value: number | undefined;
  extra?: React.ReactNode;
  icon: LucideIcon;
  linkTo: string;
  linkText: string;
  loading?: boolean;
}) {
  return (
    <Card>
      <CardContent className="p-5">
        <div className="flex items-center justify-between mb-4">
          <span className="text-[13px] text-muted-foreground font-medium">{label}</span>
          <div className="w-10 h-10 rounded-full bg-secondary flex items-center justify-center">
            <Icon className="w-5 h-5 text-foreground" strokeWidth={1.75} />
          </div>
        </div>
        <div className="flex items-baseline">
          {loading ? (
            <Skeleton className="w-16 h-10 rounded-md" />
          ) : (
            <span className="text-[32px] font-bold leading-none">{value ?? 0}</span>
          )}
          {extra}
        </div>
        <Link to={linkTo} className="mt-3 inline-flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground">
          {linkText}
          <ChevronRight className="w-3 h-3" />
        </Link>
      </CardContent>
    </Card>
  );
}

function QueueStatusChip({ status }: { status: string }) {
  if (status === "running")
    return (
      <Chip tone="success">
        <StatusDot tone="success" pulse size="xs" />
        运行中
      </Chip>
    );
  if (status === "pending")
    return (
      <Chip tone="warning">
        <StatusDot tone="warning" size="xs" />
        等待中
      </Chip>
    );
  return (
    <Chip tone="default">
      <StatusDot tone="muted" size="xs" />
      已暂停
    </Chip>
  );
}

function SystemRow({
  icon,
  label,
  ok,
  onText,
  offText,
  neutralOff,
  warnOff,
}: {
  icon: React.ReactNode;
  label: string;
  ok: boolean;
  onText: string;
  offText: string;
  neutralOff?: boolean;
  warnOff?: boolean;
}) {
  const dotTone = ok ? "success" : warnOff ? "warning" : neutralOff ? "muted" : "danger";
  return (
    <div className="flex justify-between items-center px-3 py-2.5 rounded-lg hover:bg-secondary transition-colors">
      <div className="inline-flex items-center gap-2 text-[13px]">
        <span className="text-muted-foreground">{icon}</span>
        <span>{label}</span>
      </div>
      <div className="inline-flex items-center gap-1.5">
        <StatusDot tone={dotTone as any} pulse={ok} size="xs" />
        <span className={`text-xs ${ok ? "font-medium" : "text-muted-foreground"}`}>{ok ? onText : offText}</span>
      </div>
    </div>
  );
}

function ActivityRow({ log }: { log: LogEntry }) {
  const tone = log.level === "ERROR" ? "danger" : log.level === "WARNING" ? "warning" : "success";
  const Icon = log.level === "ERROR" ? Info : log.level === "WARNING" ? Bell : CheckCircle2;
  return (
    <div className="flex items-start gap-3">
      <div className={`w-7 h-7 rounded-full bg-${tone === "danger" ? "destructive" : tone}/10 flex items-center justify-center flex-shrink-0`}>
        <Icon className={`w-3.5 h-3.5 text-${tone === "danger" ? "destructive" : tone}`} />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-[13px] font-medium truncate" title={log.message}>{log.message}</p>
        <p className="text-[11px] text-muted-foreground truncate mt-0.5">[{log.source}] · {formatRelativeTime(log.timestamp)}</p>
      </div>
    </div>
  );
}

import { Calendar } from "lucide-react";
function formatRelativeTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime();
  const min = Math.floor(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  return `${Math.floor(hr / 24)} 天前`;
}
