import { createFileRoute } from "@tanstack/react-router";
import { Settings as SettingsIcon, KeyRound, Globe, Send, Database, Save, AlertTriangle, CheckCircle2, Plus, Star, RotateCw, Trash2, Pencil, BellRing, RefreshCw, Radio, Network, Radar, Ban, Timer } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { LoadFailed, LoadFailedBanner } from "@/components/common/LoadFailed";
import { PageHeader } from "@/components/common/PageHeader";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/common/Skeleton";
import { Chip } from "@/components/common/Chip";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import {
  RETRY_INTERVAL,
  useSettings,
  useSaveSettings,
  useCacheInfo,
  useClearCache,
  useTelegramPoller,
  type SettingsConfig,
} from "@/hooks/use-settings";
import { getApiSecretKey, setApiSecretKey } from "@/lib/api";
import { useNotifyChannels, useTestNotification } from "@/hooks/use-notify-channels";
import { cn } from "@/lib/utils";
import { OVH_SUBSIDIARIES } from "@/lib/ovh-subsidiaries";
import { apiBaseUrlForEndpoint } from "@/lib/ovh-regions";
import { OvhTokenGuide } from "@/components/common/OvhTokenGuide";
import {
  useAccounts,
  useCreateAccount,
  useUpdateAccount,
  useDeleteAccount,
  useSetDefaultAccount,
  useVerifyAccount,
  useCheckAccountProxy,
  accountChipColor,
  type OVHAccount,
  type AccountInput,
  type AccountProxyCheckResult,
} from "@/hooks/use-accounts";

/** 根据 zone 推 endpoint */
function endpointForZone(zone: string): string {
  return OVH_SUBSIDIARIES.find((s) => s.code === zone)?.endpoint || "ovh-eu";
}

/** API 设置：左 sub-nav 200px + 右 form sections */
export const Route = createFileRoute("/settings")({
  component: SettingsPage,
});

const SECTIONS = [
  { id: "password", icon: KeyRound, label: "访问密码" },
  { id: "accounts", icon: Globe, label: "OVH 账户" },
  { id: "purchase", icon: Timer, label: "抢购" },
  { id: "telegram", icon: Send, label: "Telegram" },
  { id: "notify", icon: BellRing, label: "通知通道" },
  { id: "cache", icon: Database, label: "缓存管理" },
] as const;

function SettingsPage() {
  const cfg = useSettings();
  const save = useSaveSettings();
  const [active, setActive] = useState<typeof SECTIONS[number]["id"]>("password");
  const [form, setForm] = useState<SettingsConfig>({});
  const [apiKey, setApiKey] = useState("");
  // 配置到底有没有读到手。没读到就绝不能保存 —— 见 onSave 里的说明。
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (cfg.data) {
      setForm(cfg.data);
      setLoaded(true);
    }
  }, [cfg.data]);

  useEffect(() => {
    setApiKey(getApiSecretKey() || "");
  }, []);

  // 值类型按 key 取:抢购间隔是 number | undefined,其余是 string
  const set = <K extends keyof SettingsConfig>(k: K, v: SettingsConfig[K]) =>
    setForm((prev) => ({ ...prev, [k]: v }));

  const onSave = () => {
    // 配置没读到手的时候,form 还停在初始的 {} —— 所有输入框看上去都是"未配置"。
    // 这时候按保存,等于拿一份空配置去覆盖后端真实的 Telegram Token / Chat ID /
    // 用户白名单。这不是显示错误,是直接把用户的配置删了,而且他自己看不出来
    // (界面本来就显示空,保存完还是空)。所以读失败时这个按钮必须是禁用的。
    // 访问密码只写 localStorage,不经过后端配置,配置读失败也照存不误
    if (apiKey) setApiSecretKey(apiKey);
    if (!loaded) {
      toast.error("配置还没读取成功，已跳过后端配置的保存（避免用空值覆盖）");
      return;
    }
    // 提交前根据 zone 自动同步 endpoint，避免两者不一致
    const zone = form.zone || "IE";
    save.mutate({ ...form, zone, endpoint: endpointForZone(zone) });
  };

  // 访问密码那一节只写 localStorage,不碰后端配置,所以配置读失败也能改。
  const savableSection = active === "password" || loaded;

  return (
    <div className="space-y-3 sm:space-y-6">
      <PageHeader
        icon={SettingsIcon}
        title="API 设置"
        description="配置 OVH API 和通知设置"
        action={
          <Button
            onClick={onSave}
            disabled={save.isPending || !savableSection}
            title={savableSection ? undefined : "配置尚未读取成功,现在保存会用空值覆盖后端已有的配置"}
          >
            <Save className="w-4 h-4" />
            {save.isPending ? "保存中..." : "保存设置"}
          </Button>
        }
      />

      <div className="grid grid-cols-1 lg:grid-cols-[200px_1fr] gap-4">
        {/* sub-nav:桌面竖向左栏,手机横向滚动 tab */}
        {/* 手机端这是一条横滑的 tab 条。原来右边直接被裁断,「通知通道」只露半个字,
            看不出还能往右滑 —— 加一层右侧渐隐当作可滚动的提示,
            并且隐藏滚动条(移动端本来就不显示,桌面横滑时那条也碍眼)。 */}
        <div className="relative lg:contents">
          {/* nav 用了 -mx-3 出血到屏幕边,渐隐也要跟着 -right-3,
              否则它只盖到栅格格子的边界,真正被裁断的那几像素还是硬切 */}
          <div className="pointer-events-none absolute -right-3 top-0 bottom-0 w-10 bg-gradient-to-l from-background via-background/80 to-transparent lg:hidden z-10" />
        <nav className="lg:space-y-1 flex lg:flex-col overflow-x-auto lg:overflow-visible gap-1 lg:gap-0 -mx-3 px-3 lg:mx-0 lg:px-0 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
          {SECTIONS.map((s) => {
            const Icon = s.icon;
            const a = active === s.id;
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => setActive(s.id)}
                className={cn(
                  "flex items-center gap-2 px-3 py-2 rounded-md text-[13px] transition-colors whitespace-nowrap flex-shrink-0",
                  "lg:w-full lg:border-l-2",
                  a
                    ? "bg-secondary text-foreground font-medium lg:border-l-foreground"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground lg:border-l-transparent"
                )}
              >
                <Icon className="w-4 h-4" />
                {s.label}
              </button>
            );
          })}
        </nav>
        </div>

        {/* 右内容 */}
        <Card>
          <CardContent className="p-3 sm:p-6">
            {cfg.isPending ? (
              <Skeleton className="h-64 rounded-2xl" />
            ) : cfg.isError && active !== "password" && active !== "accounts" && active !== "cache" ? (
              // 配置读失败时,Telegram / 通知通道那些输入框会全渲染成空 ——
              // 看上去就是"你还没配过",而实际上后端存着真实配置。
              // 在这里直接换成失败态,顺便挡住"照着空表单点保存"这条把配置删干净的路。
              <LoadFailed
                icon={SettingsIcon}
                title="配置读取失败"
                error={cfg.error}
                onRetry={() => cfg.refetch()}
                compact
              />
            ) : active === "password" ? (
              <Section title="访问密码 / API Secret Key">
                <Field label="访问密码 *" hint="后端 .env 中的 API_SECRET_KEY，本地仅保存在 localStorage">
                  <Input
                    type="password"
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    placeholder="输入访问密码"
                  />
                </Field>
              </Section>
            ) : active === "accounts" ? (
              <AccountsSection />
            ) : active === "telegram" ? (
              <TelegramSection form={form} set={set} />
            ) : active === "notify" ? (
              <NotifySection form={form} set={set} />
            ) : active === "purchase" ? (
              <PurchaseSection form={form} set={set} />
            ) : (
              <CacheSection />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

/**
 * 抢购参数。
 *
 * 这两个间隔以前是散在四条入队路径里的字面量（30 / 30 / 30 / 2），用户既改不了、
 * 前端弹窗显示的默认值（60）还跟后端实际用的（30）对不上。现在统一读这里。
 */
function PurchaseSection({
  form,
  set,
}: {
  form: SettingsConfig;
  set: <K extends keyof SettingsConfig>(k: K, v: SettingsConfig[K]) => void;
}) {
  /** 秒数输入：允许中途空串（正在删改），失焦/提交时后端会把 0 夹回默认值 */
  const numField = (k: "defaultRetryInterval" | "quickOrderRetryInterval", fallback: number) => (
    <Input
      type="text"
      inputMode="numeric"
      value={form[k] === undefined ? "" : String(form[k])}
      placeholder={`默认 ${fallback}`}
      onChange={(e) => {
        const v = e.target.value;
        if (v === "") return set(k, undefined);
        if (/^\d+$/.test(v)) set(k, Number(v));
      }}
    />
  );

  const invalid = (v?: number) =>
    v !== undefined && (v < RETRY_INTERVAL.min || v > RETRY_INTERVAL.max);

  return (
    <Section title="抢购参数">
      <Field
        label="新任务默认重试间隔（秒）"
        hint={`网页新建任务、Telegram /buy、上架通知里的一键下单按钮都用它。留空 = ${RETRY_INTERVAL.defaultTask} 秒。范围 ${RETRY_INTERVAL.min} ~ ${RETRY_INTERVAL.max}。`}
      >
        {numField("defaultRetryInterval", RETRY_INTERVAL.defaultTask)}
        {invalid(form.defaultRetryInterval) && (
          <p className="text-[11px] text-destructive mt-1">
            要在 {RETRY_INTERVAL.min} ~ {RETRY_INTERVAL.max} 之间
          </p>
        )}
      </Field>

      <Field
        label="监控自动下单间隔（秒）"
        hint={`/watch 自动抢触发的任务用这个。货刚出现那一刻窗口可能只有几十秒，所以默认比普通任务激进（${RETRY_INTERVAL.defaultQuick} 秒）；但太密会吃 OVH 的 429，自己权衡。`}
      >
        {numField("quickOrderRetryInterval", RETRY_INTERVAL.defaultQuick)}
        {invalid(form.quickOrderRetryInterval) && (
          <p className="text-[11px] text-destructive mt-1">
            要在 {RETRY_INTERVAL.min} ~ {RETRY_INTERVAL.max} 之间
          </p>
        )}
      </Field>

      <div className="rounded-2xl border border-border bg-secondary/30 px-4 py-3 text-[12px] text-muted-foreground">
        只影响<b className="text-foreground">之后新建</b>的任务。已经在队列里跑的任务各自带着自己的间隔，
        要改单个任务去「抢购队列」页点那条任务的秒数。
      </div>
    </Section>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-5">
      <h2 className="text-base font-semibold">{title}</h2>
      <div className="space-y-4">{children}</div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-[13px] font-medium mb-1.5">{label}</label>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground mt-1">{hint}</p>}
    </div>
  );
}

/**
 * 通知通道。
 *
 * 存在的理由：以前 Telegram 是唯一通道，而监控在 Telegram 校验失败时会**自动停止** ——
 * bot 被封、token 过期、机器连不上 api.telegram.org，任何一种情况下用户失去的
 * 不是一条消息，而是整个监控，且只有翻日志才知道。现在只要还有一条通道能用，监控就继续跑。
 */
function NotifySection({
  form,
  set,
}: {
  form: SettingsConfig;
  set: (k: keyof SettingsConfig, v: string) => void;
}) {
  const channels = useNotifyChannels(true);
  const test = useTestNotification();

  return (
    <Section title="通知通道">
      <div className="rounded-2xl border border-border p-4 space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-[13px] font-medium">当前状态</h3>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => channels.refetch()}
            disabled={channels.isFetching}
          >
            <RefreshCw className={cn("w-3.5 h-3.5", channels.isFetching && "animate-spin")} />
            重新检测
          </Button>
        </div>
        {channels.isPending ? (
          <p className="text-[12px] text-muted-foreground">检测中…</p>
        ) : channels.isError ? (
          // 检测请求本身挂了。以前这里会渲染成一片空白 —— 既没有通道列表,
          // 下面那条"一条可用通道都没有"的警告也因为守卫里带了 channels.data 而不出现。
          // 用户看到的是"什么都没有",而不是"没检测成功"。
          <LoadFailedBanner
            title="通道检测失败，下面的状态不代表通道真的不可用"
            error={channels.error}
            onRetry={() => channels.refetch()}
          />
        ) : (
          <div className="space-y-1.5">
            {(channels.data?.channels || []).map((c) => (
              <div key={c.name} className="flex items-start gap-2 text-[12px]">
                {!c.configured ? (
                  <span className="mt-1.5 w-1.5 h-1.5 rounded-full bg-muted-foreground/40 flex-shrink-0" />
                ) : c.ok ? (
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 flex-shrink-0 mt-0.5" />
                ) : (
                  <AlertTriangle className="w-3.5 h-3.5 text-amber-600 flex-shrink-0 mt-0.5" />
                )}
                <span className="font-medium w-20 flex-shrink-0">{c.name}</span>
                <span className="text-muted-foreground break-all">
                  {!c.configured ? "未配置" : c.ok ? "可用" : c.detail || "不可用"}
                </span>
              </div>
            ))}
            {channels.data && !channels.data.anyAvailable && (
              <p className="text-[11px] text-amber-700 dark:text-amber-300 pt-1">
                一条可用通道都没有 —— 监控会跑不起来，也发不出补货提醒
              </p>
            )}
          </div>
        )}
      </div>

      <Field label="自定义 Webhook 地址（可选）">
        <Input
          value={form.notifyWebhookUrl || ""}
          onChange={(e) => set("notifyWebhookUrl", e.target.value)}
          placeholder="https://your.server/notify 或钉钉/飞书机器人地址"
        />
        <p className="text-[11px] text-muted-foreground mt-1">
          方向是 <b>本程序 → 这个地址</b>。发的是一个 JSON POST，同一条文本同时放进
          <code className="mx-1 px-1 rounded bg-muted">text</code>
          <code className="mr-1 px-1 rounded bg-muted">message</code>
          <code className="mr-1 px-1 rounded bg-muted">text_content.text</code>
          几个字段 —— 不猜你的接收端用哪个协议，钉钉/飞书/Bark/自建都能取到其中一个。
          注意「一键下单」按钮只有 Telegram 有，webhook 收到的是纯文本。
        </p>
      </Field>

      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => test.mutate()}
          disabled={test.isPending}
        >
          <Send className={cn("w-3.5 h-3.5", test.isPending && "animate-pulse")} />
          {test.isPending ? "发送中…" : "发一条测试通知"}
        </Button>
        <p className="text-[11px] text-muted-foreground mt-1.5">
          会往所有已配置的通道各发一条。先保存设置再测 —— 测的是已保存的配置，不是输入框里的
        </p>
      </div>
    </Section>
  );
}

/**
 * 一条轮询错误是不是"另一个进程在抢同一个 Token"。
 * 判据跟后端日志里那段保持一致(Conflict / 409),两边说法不一致会把人绕晕。
 */
function isPollConflict(err: string): boolean {
  return err.includes("Conflict") || err.includes("409");
}

function TelegramSection({
  form,
  set,
}: {
  form: SettingsConfig;
  set: (k: keyof SettingsConfig, v: string) => void;
}) {
  const poll = useTelegramPoller();

  // poller 整个对象可能缺(后端还没初始化) —— 那是"没问到状态",不是"停了"。
  // 混在一起会让用户去反复重启一个其实在正常跑的东西。
  const poller = poll.data?.poller;
  const hasToken = poll.data?.hasToken === true;

  return (
    <Section title="Telegram 通知">
      {/* 收 update 只有长轮询一条路。webhook 那条已经删掉了:
          它要公网 HTTPS 域名 + 受信证书,还得把回调端点放进鉴权白名单,
          于是只能靠 secret_token 证明来源 —— 一整套只为解决"入站端点会被伪造"
          这一个问题的东西。没有入站端点,这些连同它们的出错面一起消失了。 */}
      <div className="rounded-2xl border border-border p-4 space-y-2.5 text-[13px]">
        <div className="flex items-center justify-between">
          <h3 className="text-[13px] font-medium flex items-center gap-1.5">
            <Radio className="w-3.5 h-3.5 text-muted-foreground" />
            消息收取（长轮询）
          </h3>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => poll.refetch()}
            disabled={poll.isFetching}
          >
            <RefreshCw className={cn("w-3.5 h-3.5", poll.isFetching && "animate-spin")} />
            刷新
          </Button>
        </div>
        <p className="text-[11px] text-muted-foreground">
          程序主动去 Telegram 拉消息，<b>不需要公网地址和证书</b>，只要这台机器能访问
          api.telegram.org。家宽、NAT 后面、没域名的机器都能用一键下单。
        </p>

        {poll.isPending ? (
          <Skeleton className="h-20 rounded-xl" />
        ) : poll.isError ? (
          <LoadFailedBanner
            title="长轮询状态没读到 —— 下面是空的不代表它停了"
            error={poll.error}
            onRetry={() => poll.refetch()}
          />
        ) : !hasToken ? (
          <p className="text-[12px] text-warning">
            还没保存 Bot Token，收取器不会启动。填好下面的 Token 和 Chat ID 再点「保存设置」。
          </p>
        ) : !poller ? (
          // 后端没带 poller 回来 —— 看不到状态,不是停了
          <p className="text-[12px] text-muted-foreground">
            后端没有返回收取器状态，这里看不出它是不是真的在收消息（一般是后端版本太老或刚启动）。
          </p>
        ) : (
          <>
            <InfoRow
              label="运行状态"
              value={
                poller.running ? (
                  <Chip tone="success">
                    <CheckCircle2 className="w-3 h-3" />
                    运行中
                  </Chip>
                ) : (
                  <Chip tone="danger">
                    <AlertTriangle className="w-3 h-3" />
                    已停止
                  </Chip>
                )
              }
            />
            <InfoRow
              label="最近一次拉取"
              value={
                poller.lastPollAt ? (
                  <span className="font-mono text-[12px]">
                    {new Date(poller.lastPollAt).toLocaleString("zh-CN")}
                  </span>
                ) : (
                  <span className="text-muted-foreground">还没拉到过</span>
                )
              }
            />
            <InfoRow
              label="已确认 update_id"
              value={<span className="font-mono text-[12px]">{poller.offset}</span>}
            />
            {poller.lastError ? (
              <div className="rounded-xl border border-destructive/40 bg-destructive/5 px-3 py-2 text-[12px] flex items-start gap-2">
                <AlertTriangle className="w-4 h-4 text-destructive flex-shrink-0 mt-0.5" />
                <div className="min-w-0">
                  <p className="font-semibold text-destructive">上次拉取报错</p>
                  <p className="mt-0.5 break-words">{poller.lastError}</p>
                  {isPollConflict(poller.lastError) && (
                    <p className="mt-1.5 text-destructive">
                      这是<b>同一个 Bot Token 有另一个进程也在收</b>：两边会互相把对方踢下线，
                      表现就是「一键下单」按钮时灵时不灵、消息随机丢。
                      先停掉另一份程序（另一台机器 / 另一个容器 / 本地调试进程），
                      或者给这一份换一个 Bot Token。
                    </p>
                  )}
                </div>
              </div>
            ) : (
              <InfoRow
                label="错误状态"
                value={
                  <Chip tone="success">
                    <CheckCircle2 className="w-3 h-3" />
                    正常
                  </Chip>
                }
              />
            )}
            {!poller.running && (
              <p className="text-[11px] text-destructive">
                收取器没在跑 —— 现在一条命令、一个按钮都收不到。看上面的报错，或者重启程序。
              </p>
            )}
          </>
        )}
      </div>
      <Field label="Bot Token">
        <Input
          type="password"
          value={form.tgToken || ""}
          onChange={(e) => set("tgToken", e.target.value)}
          placeholder="123456:ABCdef..."
        />
        <p className="text-[11px] text-muted-foreground mt-1">
          换 Token 并保存后会自动重启收取器，不需要手动操作。
        </p>
      </Field>
      <Field label="Chat ID">
        <Input
          value={form.tgChatId || ""}
          onChange={(e) => set("tgChatId", e.target.value)}
          placeholder="-1001234567890"
        />
      </Field>
      <Field label="用户白名单（User ID，逗号分隔）">
        <Input
          value={form.tgAllowedUserIds || ""}
          onChange={(e) => set("tgAllowedUserIds", e.target.value)}
          placeholder="例如: 123456789, 987654321"
        />
        <p className="text-[11px] text-muted-foreground mt-1">
          只处理名单内用户的文字命令和按钮回调；留空会拒绝所有 Telegram 用户。请填写稳定的数字 User ID，不支持用户名。
        </p>
      </Field>
    </Section>
  );
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex justify-between items-start gap-3">
      <span className="text-muted-foreground flex-shrink-0">{label}</span>
      <span className="font-medium text-right min-w-0">{value}</span>
    </div>
  );
}

function CacheSection() {
  const info = useCacheInfo();
  const clear = useClearCache();
  const sqliteUpdated = info.data?.sqlite?.updatedAtMs
    ? new Date(info.data.sqlite.updatedAtMs).toLocaleString("zh-CN")
    : "从未刷新";
  return (
    <Section title="缓存管理">
      {info.isPending ? (
        <Skeleton className="h-32 rounded-2xl" />
      ) : info.isError ? (
        // 这五行以前会在请求失败时全部渲染成假读数:条数 0、状态"已过期"、
        // "从未刷新"、路径"—"。用户据此去点"清除全部",清的是一份他根本没看清的东西。
        <LoadFailed
          icon={Database}
          title="缓存信息读取失败"
          error={info.error}
          onRetry={() => info.refetch()}
          compact
        />
      ) : (
        <div className="border border-border rounded-2xl p-4 space-y-2.5 text-[13px]">
          <Row label="内存缓存条数" value={info.data?.backend?.serverCount ?? 0} />
          <Row label="内存缓存状态" value={info.data?.backend?.cacheValid ? "有效" : "已过期"} />
          <Row label="SQLite 缓存条数" value={info.data?.sqlite?.serverCount ?? 0} />
          <Row label="SQLite 最近刷新" value={<span className="text-[12px]">{sqliteUpdated}</span>} />
          <Row
            label="数据库位置"
            value={
              <code className="text-[11px] font-mono">
                {info.data?.sqlite?.path || info.data?.storage?.dataDir || "—"}
              </code>
            }
          />
        </div>
      )}
      <p className="text-[11px] text-muted-foreground">
        缓存只指 OVH 服务器目录。订阅 / 队列 / 历史 等业务数据不在此清理范围内。
      </p>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => clear.mutate("memory")} disabled={clear.isPending}>
          清除内存缓存
        </Button>
        <Button variant="outline" onClick={() => clear.mutate("sqlite")} disabled={clear.isPending}>
          清除 SQLite 缓存
        </Button>
        <Button variant="destructive" onClick={() => clear.mutate("all")} disabled={clear.isPending}>
          清除全部
        </Button>
      </div>
    </Section>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex justify-between items-center gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium text-right">{value}</span>
    </div>
  );
}

// ─── 账户管理 ───────────────────────────────────────────────────────────────

function AccountsSection() {
  // 出口 IP 的实测在后端做:保存代理账户时同步验一轮,此后后台每 30 秒强制复检
  // (RefreshOutboundIPChecks)。这里跟着 30 秒轮询,卡片上的出口 IP / 阻断状态才不会停在过去。
  const accounts = useAccounts({ refetchInterval: 30_000 });
  const [showAdd, setShowAdd] = useState(false);
  const [editAcc, setEditAcc] = useState<OVHAccount | null>(null);
  const list = accounts.data || [];

  // 各账户实测到的出口 IP,按 IP 归堆。两个代理账户撞在同一个出口 IP 上,
  // 说明"隔离"压根没生效 —— 只有把多个账户摆在一起才看得出来。
  // 数据源是后端实测写入列表的 actualOutboundIp(只有配了代理的账户才有)。
  const byIP = new Map<string, string[]>();
  list.forEach((a) => {
    if (!a.actualOutboundIp) return;
    byIP.set(a.actualOutboundIp, [...(byIP.get(a.actualOutboundIp) || []), a.name]);
  });
  const collisions = Array.from(byIP.entries()).filter(([, names]) => names.length > 1);

  return (
    <Section title="OVH 账户管理">
      <div className="flex items-start justify-between gap-3">
        <p className="text-[12px] text-muted-foreground">
          每个 OVH 账户(凭据)单独保存,抢购队列 / 狙击 / 订阅创建时各自指定账户。删账户会一并清除关联的 queue / history / sniper tasks。
        </p>
        <Button onClick={() => setShowAdd(true)} size="sm" className="flex-shrink-0">
          <Plus className="w-4 h-4" />
          添加账户
        </Button>
      </div>

      {/* 为什么每个账户要有自己的出口 —— 不讲清楚,代理这一栏看起来只是个可选项 */}
      <div className="rounded-xl border border-border bg-secondary/30 px-3 py-2.5 space-y-1.5 text-[11px] leading-relaxed">
        <p className="font-semibold flex items-center gap-1.5">
          <Network className="w-3.5 h-3.5" />
          每个账户可以配自己的出站代理
        </p>
        <p className="text-muted-foreground">
          OVH 的限流按来源 IP 算。多个账户共用一个出口时,一个账户被限流会把其它账户一起拖下水 ——
          而这恰好发生在补货那一刻,也就是唯一要紧的时刻。
        </p>
        <p className="text-muted-foreground">
          代理配错或连不上时,后端<b className="text-warning">不会</b>退回直连,请求直接失败。这是故意的 ——
          悄悄直连的表现是一切正常、隔离却已经没了,而你无从察觉。
        </p>
      </div>

      {collisions.length > 0 && (
        <div className="rounded-xl border border-destructive/40 bg-destructive/5 px-3 py-2.5 text-[11px] space-y-1">
          <p className="font-semibold flex items-center gap-1.5 text-destructive">
            <AlertTriangle className="w-3.5 h-3.5" />
            这些账户实测出口 IP 相同 —— 配的代理没把出口分开
          </p>
          {collisions.map(([ip, names]) => (
            <p key={ip} className="text-muted-foreground">
              <span className="font-mono font-semibold text-foreground">{ip}</span> ← {names.join("、")}
            </p>
          ))}
          <p className="text-muted-foreground">
            它们在 OVH 眼里是同一个来源,限流会互相拖累 —— 一个被限,其它一起被限。
            去编辑里确认各自的代理是不是写成了同一个出口(或压根没生效),改好再测一次。
          </p>
        </div>
      )}

      {accounts.isError ? (
        // 说成"还没有账户"会让用户重新粘一遍 OVH 三件套凭据,
        // 而后端其实存着好好的 —— 重复添加只会多出一个重名账户。
        <LoadFailed
          icon={Globe}
          title="账户列表读取失败"
          error={accounts.error}
          onRetry={() => accounts.refetch()}
          compact
        />
      ) : accounts.isPending ? (
        <div className="space-y-2">
          {Array.from({ length: 2 }).map((_, i) => (
            <Skeleton key={i} className="h-24 rounded-2xl" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <Card>
          <CardContent className="p-8 text-center text-sm text-muted-foreground">
            还没有账户,点右上角"添加账户"创建一个
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {list.map((a) => (
            <AccountCard key={a.id} acc={a} onEdit={() => setEditAcc(a)} />
          ))}
        </div>
      )}

      {showAdd && <AccountDialog onClose={() => setShowAdd(false)} />}
      {editAcc && <AccountDialog acc={editAcc} onClose={() => setEditAcc(null)} />}
    </Section>
  );
}

function AccountCard({ acc, onEdit }: { acc: OVHAccount; onEdit: () => void }) {
  const setDefault = useSetDefaultAccount();
  const del = useDeleteAccount();
  const verify = useVerifyAccount();
  const [confirming, setConfirming] = useState(false);

  // 出口 IP 由后端保证新鲜:保存代理账户时同步验一轮,此后后台每 30 秒强制复检,
  // 列表 30 秒轮询把结果带到这张卡上。mismatch / failed 时这个账户的带签名 OVH 请求
  // 会被 GuardedTransport 直接阻断 —— 不是"暂停任务",恢复后自动放行,这里必须说清楚。
  const blocked = !!acc.proxyUrl && (acc.outboundIpStatus === "mismatch" || acc.outboundIpStatus === "failed");

  // 后端 /accounts/:id/verify 除了 valid 还会带 subsidiaryWarning:
  // zone(决定目录站点/币种/下单 region)与 OVH /me 的 ovhSubsidiary 不一致时,凭据依然有效,
  // 但每一次调用都会打到错误的站点。toast 会消失,这里再常驻一条,免得用户点完验证就忘了。
  const subsidiaryWarning = verify.data?.subsidiaryWarning;

  return (
    <div className="border border-border rounded-2xl p-4 flex flex-col gap-3">
      <div className="flex flex-col sm:flex-row sm:items-center gap-3">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 mb-1 flex-wrap">
          <span className="font-semibold text-sm">{acc.name}</span>
          <span className={cn("inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium", accountChipColor(acc.zone))}>
            {acc.zone}
          </span>
          {acc.isDefault && (
            <Chip tone="success">
              <Star className="w-3 h-3" />
              默认
            </Chip>
          )}
        </div>
        <div className="text-[11px] text-muted-foreground flex items-center gap-2 flex-wrap font-mono">
          <span>{acc.endpoint}</span>
          <span>·</span>
          <span>{acc.iam}</span>
          <span>·</span>
          <span>建于 {new Date(acc.createdAt).toLocaleDateString("zh-CN")}</span>
        </div>
        {/* 出站配置 + 后端实测到的出口 IP。IP 放在这里就是为了几个账户之间横向比对 */}
        <div className="flex items-center gap-1.5 flex-wrap mt-1.5">
          {acc.proxyUrl ? (
            <Chip tone="info">
              <Network className="w-3 h-3" />
              <span className="font-mono">{acc.proxyUrl}</span>
            </Chip>
          ) : (
            <Chip>直连</Chip>
          )}
          {acc.actualOutboundIp ? (
            <Chip
              tone={acc.outboundIpStatus === "verified" ? "success" : "danger"}
              title={
                acc.outboundIpError ||
                (acc.outboundIpCheckedAt ? `实测于 ${new Date(acc.outboundIpCheckedAt).toLocaleString("zh-CN")}` : undefined)
              }
            >
              出口 <span className="font-mono font-semibold">{acc.actualOutboundIp}</span>
            </Chip>
          ) : acc.proxyUrl && acc.outboundIpStatus === "failed" ? (
            <Chip tone="danger" title={acc.outboundIpError}>出口检测失败</Chip>
          ) : acc.proxyUrl ? (
            <span className="text-[11px] text-muted-foreground">出口 IP 待检查(后台每 30 秒自动测)</span>
          ) : null}
        </div>
      </div>
      <div className="flex items-center gap-2 flex-shrink-0">
        <Button variant="ghost" size="icon" onClick={() => verify.mutate(acc.id)} disabled={verify.isPending} title="重新验证凭据">
          <RotateCw className={cn("w-4 h-4", verify.isPending && "animate-spin")} />
        </Button>
        {!acc.isDefault && (
          <Button variant="ghost" size="icon" onClick={() => setDefault.mutate(acc.id)} disabled={setDefault.isPending} title="设为默认">
            <Star className="w-4 h-4" />
          </Button>
        )}
        <Button variant="ghost" size="icon" onClick={onEdit} title="编辑">
          <Pencil className="w-4 h-4" />
        </Button>
        <Button variant="ghost" size="icon" onClick={() => setConfirming(true)} title="删除" className="text-destructive hover:text-destructive">
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>
      </div>

      {/* 代理出口没验过的账户,它的带签名请求已经被后端阻断了 —— 用户看到的现象只是"这个账户一单都下不出去" */}
      {blocked && (
        <p className="text-[11px] text-destructive border border-destructive/40 bg-destructive/5 rounded-xl px-3 py-2">
          ⚠ {acc.outboundIpError || "出口 IP 未通过验证"} —— 该账户的 OVH 请求现在会被后端阻断(不会退回直连);
          代理恢复、复检通过后自动放行,不用手动处理。
        </p>
      )}

      {subsidiaryWarning && (
        <p className="text-[11px] text-warning border border-warning/40 bg-warning/5 rounded-xl px-3 py-2">
          ⚠ 子公司配置与 OVH 实际归属不一致：{subsidiaryWarning}
        </p>
      )}

      <Dialog open={confirming} onOpenChange={setConfirming}>
        <DialogContent className="w-[95vw] sm:w-full sm:max-w-md">
          <DialogHeader>
            <DialogTitle>确认删除账户 {acc.name}?</DialogTitle>
            <DialogDescription className="text-destructive">
              将级联删除该账户的所有 queue 任务、history 历史、config_sniper 任务。
              监控订阅的 auto_order 引用此账户的会清空。该操作不可逆。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirming(false)}>取消</Button>
            <Button
              variant="destructive"
              onClick={async () => {
                await del.mutateAsync(acc.id);
                setConfirming(false);
              }}
              disabled={del.isPending}
            >
              确认删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/**
 * 代理地址的前置校验,规则跟后端 netfp.ValidateProxyURL 一致(协议 + 主机 + 端口)。
 * 后端才是权威,这里只是让用户在按保存之前就看见错在哪 ——
 * 代理写错的代价不是一句报错,是这个账户在补货那一刻一单都下不出去。
 */
function proxyInputError(raw: string): string {
  const v = raw.trim();
  if (!v) return "";
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return "解析不了。格式:socks5://用户名:密码@主机:端口";
  }
  const scheme = u.protocol.replace(":", "").toLowerCase();
  if (!["http", "https", "socks5", "socks5h"].includes(scheme)) {
    return `不支持的协议 ${scheme}:只支持 http / https / socks5 / socks5h`;
  }
  if (!u.hostname) return "缺少主机名";
  if (!u.port) return "缺少端口 —— 必须显式写出来,例如 :1080";
  return "";
}

/**
 * 出口检测结果面板。
 *
 * 这是用户唯一能确认"隔离真的生效"的手段,所以 IP 要显眼到能一眼跟另一个账户比对。
 * 三种状态必须分开:请求没发出去(我们没问到)、测了但失败(出口不对/断了)、测到了。
 */
function EgressPanel({
  result,
  pending,
  requestError,
  onRetry,
}: {
  result?: AccountProxyCheckResult | null;
  pending: boolean;
  /** mutation 本身失败(网络断开/后端出错且响应里没有检测结论)—— 跟"代理不通"是两回事 */
  requestError?: unknown;
  onRetry: () => void;
}) {
  if (pending) return <Skeleton className="h-24 rounded-2xl" />;
  if (requestError) {
    return (
      <LoadFailedBanner
        title="出口检测请求没发出去(这不代表代理有问题)"
        error={requestError}
        onRetry={onRetry}
      />
    );
  }
  if (!result) return null;
  const at = result.outboundIpCheckedAt ? new Date(result.outboundIpCheckedAt) : null;
  const atText = at && !isNaN(at.getTime()) ? at.toLocaleString("zh-CN") : "";

  if (!result.healthy) {
    // mismatch:代理是通的、出口 IP 也查到了,只是跟预期不一致 —— 文案不可以说成"代理没通"
    const mismatch = result.outboundIpStatus === "mismatch";
    return (
      <div className="rounded-xl border border-destructive/40 bg-destructive/5 px-3 py-2.5 space-y-1 text-[11px]">
        <p className="font-semibold text-destructive flex items-center gap-1.5">
          <AlertTriangle className="w-3.5 h-3.5" />
          {mismatch ? "出口 IP 与预期不符" : "出口检测失败"}
        </p>
        {mismatch && result.actualOutboundIp ? (
          <p className="text-muted-foreground">
            实际出口 <span className="font-mono font-semibold">{result.actualOutboundIp}</span>,
            与预期的 <span className="font-mono">{result.expectedOutboundIp || "(空)"}</span> 不一致。
          </p>
        ) : (
          <p className="text-muted-foreground break-all">
            {result.outboundIpError || "代理没通,后端没给具体原因"}
          </p>
        )}
        <p className="text-muted-foreground">
          配了代理就不会退回直连 —— 这个状态下该账户的请求会被后端阻断。修好代理,或者改回直连。
        </p>
        {atText && <p className="text-muted-foreground/70">{atText} 测</p>}
      </div>
    );
  }

  if (!result.actualOutboundIp) {
    return (
      <div className="rounded-xl border border-border px-3 py-2.5 space-y-1 text-[11px]">
        <p className="font-semibold text-foreground">
          {result.proxy ? "代理连通,但没查到出口 IP" : "直连出口"}
        </p>
        <p className="text-muted-foreground">
          {result.proxy
            ? "查出口 IP 用的是第三方站点,它自己挂掉不代表代理有问题 —— 代理本身已经通了。稍后重试一次即可。"
            : "没配代理的账户共用这台机器的出口 IP,不做单独实测。"}
        </p>
        {atText && <p className="text-muted-foreground/70">{atText} 测</p>}
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-success/40 bg-success/5 px-3 py-2.5 space-y-1.5">
      <p className="text-[11px] text-muted-foreground">这个账户实际用的出口 IP</p>
      <p className="text-2xl font-mono font-semibold tracking-tight break-all">{result.actualOutboundIp}</p>
      <p className="text-[11px] text-muted-foreground">
        经由 <span className="font-mono">{result.proxy || "代理"}</span>
        {atText && <> · {atText} 测</>}
      </p>
      <p className="text-[11px] text-muted-foreground">
        拿它跟别的账户比一比:两个账户测出同一个 IP,OVH 就把它们算作同一个来源,限流互相拖累。
      </p>
    </div>
  );
}

function AccountDialog({ acc, onClose }: { acc?: OVHAccount; onClose: () => void }) {
  const create = useCreateAccount();
  const update = useUpdateAccount();
  // 出口检测:不落库、不发 OVH 鉴权请求。表单里代理留空时传 accountId,
  // 后端会回落到该账户已保存的代理与预期 IP。
  const test = useCheckAccountProxy();
  const isEdit = !!acc;

  // 已经落库的那份出站配置。「测试出口 IP」测已保存配置时用它,
  // 所以必须拿得到 id 和保存后的值 —— 新建的账户保存成功后这里才会被填上。
  const [saved, setSaved] = useState<{
    id: string;
    proxyUrl: string;
    expectedOutboundIp: string;
  } | null>(
    acc
      ? {
          id: acc.id,
          proxyUrl: acc.proxyUrl || "",
          expectedOutboundIp: acc.expectedOutboundIp || "",
        }
      : null
  );

  // 编辑时三个凭据一律留空。后端不再下发明文（只给掩码），
  // 留空 = 保持原值（UpdateAccount 本来就是这个语义）。
  // 以前这里回填明文，等价于让 GET /api/accounts 必须吐出可用的凭据 ——
  // 那正是「任意网页读走 OVH 凭据」那条链的最后一环。
  const [form, setForm] = useState({
    name: acc?.name || "",
    appKey: "",
    appSecret: "",
    consumerKey: "",
    expectedOutboundIp: acc?.expectedOutboundIp || "",
    zone: acc?.zone || "IE",
    // 代理地址同理,也**不预填**:GET 回来的是打过码的(密码变 ***),
    // 原样提交会把 *** 存成密码,下次抢购就连不上代理了。
    proxyUrl: "",
  });
  // 「改回直连」标记。清代理只能靠显式提交空串(后端:不传 = 不改,"" = 清掉),
  // 而输入框留空是"保持不变" —— 没有这个开关,代理一旦配上就再也摘不掉了。
  const [clearProxy, setClearProxy] = useState(false);
  const set = (k: keyof typeof form, v: string) => setForm((p) => ({ ...p, [k]: v }));

  // 最近一次出口检测测到的 IP —— 「使用当前 IP」一键回填用
  const testedIP = test.data?.actualOutboundIp || "";
  const proxyErr = clearProxy ? "" : proxyInputError(form.proxyUrl);
  // 将要提交一个新代理。后端对"配了代理"有硬校验:预期出口 IP 必须是合法 IPv4,
  // 空着提交直接 400 —— 在这里先拦下,别让用户白跑一趟
  const proxyWillBeSet = !clearProxy && !!form.proxyUrl.trim();
  // 新建时三个凭据必填；编辑时可以全留空（只改名字/区域/出站配置）
  const canSubmit =
    !proxyErr &&
    (!proxyWillBeSet || !!form.expectedOutboundIp.trim()) &&
    (saved
      ? !!form.name.trim()
      : !!(form.name.trim() && form.appKey.trim() && form.appSecret.trim() && form.consumerKey.trim()));

  // 「测试出口 IP」要打的目标:表单里填了新代理就测新值(不落库);
  // 留空但账户已存过代理,就传空让后端回落已保存的配置;改回直连时没得测。
  // 后端硬校验要求配了代理必须给预期出口 IP,填了代理没给预期时先拦住。
  const testTargetProxy = clearProxy ? "" : form.proxyUrl.trim();
  const canTestEgress = !clearProxy && (testTargetProxy ? !!form.expectedOutboundIp.trim() : !!saved?.proxyUrl);
  const runEgressCheck = () => {
    if (!canTestEgress) return;
    test.mutate({
      accountId: saved?.id,
      proxyUrl: testTargetProxy,
      expectedOutboundIp: form.expectedOutboundIp.trim(),
    });
  };

  // 保存成功后把基准换成后端回来的那份:输入框回到"留空 = 不变",清除标记撤掉。
  // 不这么做的话刚保存完界面仍算"有未保存的改动",测试按钮会一直是禁用的。
  const applySaved = (a: OVHAccount) => {
    setSaved({
      id: a.id,
      proxyUrl: a.proxyUrl || "",
      expectedOutboundIp: a.expectedOutboundIp || "",
    });
    setForm((p) => ({
      ...p,
      proxyUrl: "",
      expectedOutboundIp: a.expectedOutboundIp || "",
    }));
    setClearProxy(false);
    // 保存前的检测结果是旧配置的 —— 留着会让人以为新配置已经测过
    test.reset();
  };

  const buildPayload = (): Partial<AccountInput> => {
    const payload: Partial<AccountInput> = {
      name: form.name.trim(),
      // 留空的凭据不发 —— 后端见空即保持原值
      appKey: form.appKey.trim(),
      appSecret: form.appSecret.trim(),
      consumerKey: form.consumerKey.trim(),
      zone: form.zone,
      endpoint: endpointForZone(form.zone),
    };
    if (!saved) {
      // 新建:所填即所得,空 = 直连
      payload.useDirect = clearProxy;
      payload.proxyUrl = clearProxy ? "" : form.proxyUrl.trim();
      payload.expectedOutboundIp = clearProxy ? "" : form.expectedOutboundIp.trim();
      return payload;
    }
    // 编辑:指针语义。空串只在用户明确点了「改回直连」时才发,
    // 输入框留空则整个 key 都不传 —— 否则会把已配好的代理悄悄清掉。
    if (clearProxy) {
      // 后端只认 useDirect=true 这个明确动作:它会把 proxyUrl 和预期出口 IP
      // 一起清掉。光发 proxyUrl:"" 后端视为"不改",代理永远摘不掉。
      payload.useDirect = true;
    } else {
      if (form.proxyUrl.trim()) {
        payload.proxyUrl = form.proxyUrl.trim();
      }
      const expectedIP = form.expectedOutboundIp.trim();
      if (expectedIP && expectedIP !== saved.expectedOutboundIp) {
        payload.expectedOutboundIp = expectedIP;
      }
    }
    return payload;
  };

  /** 保存,返回账户 ID(失败返回 null,错误提示由 hooks 里的 toast 负责) */
  const save = async (): Promise<string | null> => {
    if (!canSubmit) return null;
    const payload = buildPayload();
    if (saved) {
      const res = await update.mutateAsync({ id: saved.id, input: payload });
      applySaved(res.account);
      return saved.id;
    }
    const res = await create.mutateAsync(payload as AccountInput);
    applySaved(res.account);
    return res.account.id;
  };

  const submit = async () => {
    try {
      const id = await save();
      if (id) onClose();
    } catch {
      // useCreateAccount / useUpdateAccount 的 onError 已经弹过 toast
    }
  };

  // 保存 → 立刻用刚落库的配置测出口,并且**不关对话框**:
  // 用户的动作就是"我刚填完这个代理,当场看看通不通、出口是不是我要的那个"。
  // 传空 proxyUrl / expectedOutboundIp,后端按 accountId 回落刚落库的配置。
  const saveAndTest = async () => {
    try {
      const id = await save();
      if (id) test.mutate({ accountId: id, proxyUrl: "", expectedOutboundIp: "" });
    } catch {
      // 同上,toast 已经提示过
    }
  };

  const busy = create.isPending || update.isPending || test.isPending;

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="w-[95vw] sm:w-full sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? `编辑账户 ${acc!.name}` : "添加 OVH 账户"}</DialogTitle>
          <DialogDescription>填三个 OVH 密钥 + 选子公司,保存时会自动调 /me 验证凭据。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2 max-h-[65vh] overflow-y-auto -mx-6 px-6">
          <Field label="账户名称 *">
            <Input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="主号 / 小号 A" autoFocus />
            {isEdit && (
              <p className="text-[11px] text-muted-foreground mt-1">
                下面三个凭据留空即保持不变。出于安全考虑，后端不再把已保存的凭据发回浏览器
                （只显示掩码），要更换请重新填写完整值。
              </p>
            )}
          </Field>
          {/* 顺序和首次录入页一致:子公司在前 —— token 申请地址跟着它变 */}
          <Field
            label="OVH 子公司 (Zone) *"
            hint={`你的 OVH 账号注册在哪个国家/地区。Endpoint ${endpointForZone(form.zone)} · IAM go-ovh-${form.zone.toLowerCase()} 由它自动派生`}
          >
            <Select value={form.zone} onValueChange={(v) => set("zone", v)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {OVH_SUBSIDIARIES.map((s) => (
                  <SelectItem key={s.code} value={s.code}>
                    {s.code} · {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          {/* 改已有账户时不重复这块:那时用户手上早就有密钥了 */}
          {!isEdit && <OvhTokenGuide endpoint={endpointForZone(form.zone)} />}

          <Field label="APP KEY *" hint={isEdit ? undefined : "OVH 申请页上的 Application Key"}>
            <Input type="password" value={form.appKey} onChange={(e) => set("appKey", e.target.value)}
              placeholder={isEdit ? (acc?.appKey || "留空 = 不修改") : "从 OVH 申请页复制"} />
          </Field>
          <Field label="APP SECRET *" hint={isEdit ? undefined : "OVH 申请页上的 Application Secret"}>
            <Input type="password" value={form.appSecret} onChange={(e) => set("appSecret", e.target.value)}
              placeholder={isEdit ? (acc?.appSecret || "留空 = 不修改") : "从 OVH 申请页复制"} />
          </Field>
          <Field label="CONSUMER KEY *" hint={isEdit ? undefined : "OVH 申请页上的 Consumer Key"}>
            <Input type="password" value={form.consumerKey} onChange={(e) => set("consumerKey", e.target.value)}
              placeholder={isEdit ? (acc?.consumerKey || "留空 = 不修改") : "从 OVH 申请页复制"} />
          </Field>

          {/* ── 出站配置 ───────────────────────────────────────────────── */}
          <div className="border-t border-border pt-4 space-y-4">
            <div>
              <p className="text-[13px] font-semibold flex items-center gap-1.5">
                <Network className="w-3.5 h-3.5" />
                出站代理
              </p>
              <p className="text-[11px] text-muted-foreground mt-1 leading-relaxed">
                OVH 的限流按来源 IP 算,几个账户共用一个出口时会互相拖累,而这恰好发生在补货那一刻。
                给这个账户配一个自己的出口,它就不会被别的账户连累。
              </p>
            </div>

            <Field label="出站代理地址">
              <Input
                value={form.proxyUrl}
                onChange={(e) => set("proxyUrl", e.target.value)}
                disabled={clearProxy}
                placeholder={
                  clearProxy
                    ? "已标记改回直连"
                    : saved
                      ? "留空 = 保持不变"
                      : "socks5://user:pass@1.2.3.4:1080(留空 = 直连)"
                }
                className="font-mono"
              />
              {proxyErr && <p className="text-[11px] text-destructive mt-1">代理地址不合法:{proxyErr}</p>}

              {/* 已落库的账户:回显的是打过码的地址,绝不能预填进输入框;清代理要有明确动作 */}
              {saved && (
                <div className="mt-2 space-y-1.5">
                  <p className="text-[11px] text-muted-foreground">
                    当前:
                    {saved?.proxyUrl ? (
                      <code className="ml-1 font-mono">{saved.proxyUrl}</code>
                    ) : (
                      <span className="ml-1">直连(没配代理)</span>
                    )}
                    {saved?.proxyUrl ? "(密码已打码,所以这里不预填 —— 把 *** 原样提交会把它存成真密码)" : ""}
                  </p>
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-[11px] text-muted-foreground">留空 = 保持不变;要摘掉代理:</span>
                    {clearProxy ? (
                      <Button variant="outline" size="sm" onClick={() => setClearProxy(false)}>
                        撤销「改回直连」
                      </Button>
                    ) : (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={!saved?.proxyUrl}
                        onClick={() => {
                          setClearProxy(true);
                          set("proxyUrl", "");
                        }}
                      >
                        <Ban className="w-3.5 h-3.5" />
                        改回直连
                      </Button>
                    )}
                  </div>
                  {clearProxy && (
                    <p className="text-[11px] text-warning">
                      保存后这个账户会清掉代理、改成直连出口 —— 它将和其它直连账户共用同一个出口 IP。
                    </p>
                  )}
                </div>
              )}

              <div className="mt-2 space-y-1 text-[11px] leading-relaxed">
                <p className="text-muted-foreground">
                  支持 <code className="font-mono">http://</code> <code className="font-mono">https://</code>{" "}
                  <code className="font-mono">socks5://</code> <code className="font-mono">socks5h://</code>,
                  <b>必须带端口</b>(例如 <code className="font-mono">socks5://user:pass@1.2.3.4:1080</code>)。
                  {saved ? "留空 = 保持不变(要摘掉代理用上面的「改回直连」)。" : "留空 = 直连。"}
                </p>
                <p className="text-warning">
                  代理配错或连不上时<b>不会</b>退回直连 —— 该账户的带签名 OVH 请求会被后端直接阻断。
                  后台每 30 秒对配了代理的账户强制复检一次出口 IP,代理恢复、复检通过后自动放行,不用手动处理。
                  这是故意的 —— 悄悄直连的表现是一切正常、隔离却已经没了。
                </p>
              </div>
            </Field>

            {/* 预期出口 IP:后端出口监控的基准 —— 实测出口 ≠ 它时该账户的带签名请求
                会被后端阻断。配了代理必须给(后端硬校验),所以跟着代理一起出现 */}
            {(proxyWillBeSet || !!saved?.proxyUrl || !!form.expectedOutboundIp) && (
              <Field label="预期出口 IP">
                <div className="flex items-center gap-2">
                  <Input
                    value={form.expectedOutboundIp}
                    onChange={(e) => set("expectedOutboundIp", e.target.value)}
                    placeholder="例如 203.0.113.10"
                    inputMode="numeric"
                    disabled={clearProxy}
                    className="font-mono"
                  />
                  {testedIP && testedIP !== form.expectedOutboundIp && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-auto px-1.5 text-[11px] flex-shrink-0"
                      disabled={clearProxy}
                      onClick={() => set("expectedOutboundIp", testedIP)}
                    >
                      使用当前 IP
                    </Button>
                  )}
                </div>
                {proxyWillBeSet && !form.expectedOutboundIp.trim() && (
                  <p className="text-[11px] text-warning mt-1">
                    配了代理就必填 —— 后端拿它判断隔离是否真的生效,空着保存会被打回。
                  </p>
                )}
                <p className="text-[11px] text-muted-foreground mt-1">
                  这个账户的流量应该从哪个 IPv4 出去;实测出口和它不一致时,该账户的带签名 OVH 请求会被后端阻断。
                </p>
              </Field>
            )}

            {/* 测试出口:就放在代理输入框下面,填完当场点一下 */}
            {!clearProxy && (!!form.proxyUrl.trim() || !!saved?.proxyUrl) && (
              <div className="space-y-2">
                <div className="flex items-center gap-2 flex-wrap">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={runEgressCheck}
                    disabled={!canTestEgress || busy}
                  >
                    <Radar className={cn("w-3.5 h-3.5", test.isPending && "animate-pulse")} />
                    {test.isPending ? "测试中…" : "测试出口 IP"}
                  </Button>
                  <span className="text-[11px] text-muted-foreground">
                    {!canTestEgress
                      ? "填了代理就必须同时填上面的预期出口 IP —— 补上再测。"
                      : testTargetProxy
                        ? "测的是上面刚填的代理,不落库 —— 出口对了再去保存。"
                        : "测的是这个账户已保存的配置,走的和真实下单同一条出站链路。"}
                  </span>
                </div>
                <EgressPanel
                  result={test.data}
                  pending={test.isPending}
                  requestError={test.isError ? test.error : undefined}
                  onRetry={runEgressCheck}
                />
              </div>
            )}

          </div>

          {/* 申请密钥的说明放在这里而不是只放首次进入的弹窗:
              日常加号 / 换号都走这个对话框,而"去哪申请、申请错站点会怎样"恰恰是
              这时候最容易踩的坑。链接必须跟着上面选的子公司走 —— 三站的 token 互不通用。 */}
          <div className="rounded-xl border border-border bg-secondary/30 px-3 py-2.5 space-y-1.5">
            <p className="text-[11px] font-semibold">还没有密钥?</p>
            <p className="text-[11px] text-muted-foreground leading-relaxed">
              去
              <a
                href={`${apiBaseUrlForEndpoint(endpointForZone(form.zone))}/createToken/`}
                target="_blank"
                rel="noreferrer"
                className="underline mx-1 text-primary"
              >
                {apiBaseUrlForEndpoint(endpointForZone(form.zone)).replace("https://", "")}/createToken
              </a>
              申请。<b>{form.zone}</b> 属于这个站点,
              <span className="text-warning">在别的站点申请的密钥登不进去</span>(三站互不通用)。
            </p>
            <p className="text-[11px] text-muted-foreground leading-relaxed">
              权限最省事是四条全给:
              <code className="mx-1 px-1 py-0.5 rounded bg-background text-[10px]">GET POST PUT DELETE</code>
              各配 <code className="px-1 py-0.5 rounded bg-background text-[10px]">/*</code>；
              有效期选 <b>Unlimited</b> —— 到期后抢购和监控会静默失效。
            </p>
          </div>
        </div>
        {/* 三个按钮在窄屏上要能换行,否则「保存并验证」会被挤出对话框 */}
        <DialogFooter className="flex-wrap gap-2 space-x-0">
          <Button variant="outline" onClick={onClose}>取消</Button>
          {!clearProxy && (!!form.proxyUrl.trim() || !!saved?.proxyUrl) && (
            <Button variant="outline" onClick={saveAndTest} disabled={!canSubmit || busy}>
              <Radar className="w-3.5 h-3.5" />
              {(create.isPending || update.isPending) ? "保存中…" : "保存并测试出口"}
            </Button>
          )}
          <Button onClick={submit} disabled={!canSubmit || busy}>
            {(create.isPending || update.isPending) ? "保存中…" : "保存并验证"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
