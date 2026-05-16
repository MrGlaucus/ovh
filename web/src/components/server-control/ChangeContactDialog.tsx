import { useState } from "react";
import { Mail, RefreshCw, Check, X as XIcon, KeyRound } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Skeleton } from "@/components/common/Skeleton";
import { EmptyState } from "@/components/common/EmptyState";
import { Chip } from "@/components/common/Chip";
import { useChangeContact, useContactChangeRequests, useContactRequestAction } from "@/hooks/use-server-control";
import { toast } from "sonner";

/** 变更联系人对话框：提交新 NIC + 查看 / 接受 / 拒绝 / 重发邮件 待审请求 + token 子对话框 */
export function ChangeContactDialog({
  serviceName,
  open,
  onOpenChange,
}: {
  serviceName: string;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const submit = useChangeContact();
  const list = useContactChangeRequests(open);
  const action = useContactRequestAction();

  const [admin, setAdmin] = useState("");
  const [tech, setTech] = useState("");
  const [billing, setBilling] = useState("");
  const [tokenTarget, setTokenTarget] = useState<{ id: number | string; mode: "accept" | "refuse" } | null>(null);
  const [token, setToken] = useState("");

  const handleSubmit = async () => {
    if (!admin && !tech && !billing) {
      toast.error("请至少填写一个联系人字段");
      return;
    }
    try {
      await submit.mutateAsync({ serviceName, admin, tech, billing });
      toast.success("变更请求已提交，等待邮件确认");
      setAdmin("");
      setTech("");
      setBilling("");
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "提交失败");
    }
  };

  const handleAction = async (id: number | string, mode: "accept" | "refuse" | "resend", tokenVal?: string) => {
    try {
      await action.mutateAsync({ id, action: mode, token: tokenVal });
      toast.success(mode === "resend" ? "确认邮件已重发" : mode === "accept" ? "已接受" : "已拒绝");
      setTokenTarget(null);
      setToken("");
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "操作失败");
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-hidden flex flex-col">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Mail className="w-5 h-5" />
              变更联系人
            </DialogTitle>
            <DialogDescription>切换 admin / tech / billing 联系人。OVH 将向当前联系人邮箱发送确认链接。</DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="submit" className="flex-1 overflow-hidden flex flex-col">
            <TabsList>
              <TabsTrigger value="submit">提交变更</TabsTrigger>
              <TabsTrigger value="requests">待审请求</TabsTrigger>
            </TabsList>

            <TabsContent value="submit" className="overflow-y-auto -mx-6 px-6">
              <div className="space-y-3 py-2">
                <ContactField label="Admin 联系人 NIC" placeholder="例如 ab12345-ovh" value={admin} onChange={setAdmin} />
                <ContactField label="Tech 联系人 NIC" placeholder="例如 ab12345-ovh" value={tech} onChange={setTech} />
                <ContactField label="Billing 联系人 NIC" placeholder="例如 ab12345-ovh" value={billing} onChange={setBilling} />
                <p className="text-[11px] text-muted-foreground">
                  NIC handle 格式见 OVH 账户：通常是 2 字母 + 5 位数字 + `-ovh`。留空则保持原联系人。
                </p>
                <div className="pt-2">
                  <Button onClick={handleSubmit} disabled={submit.isPending}>
                    {submit.isPending ? "提交中…" : "提交变更请求"}
                  </Button>
                </div>
              </div>
            </TabsContent>

            <TabsContent value="requests" className="overflow-y-auto -mx-6 px-6">
              {list.isPending ? (
                <Skeleton className="h-40 rounded-2xl" />
              ) : (list.data || []).length === 0 ? (
                <EmptyState icon={Mail} title="暂无待审请求" />
              ) : (
                <div className="space-y-2 py-2">
                  {(list.data || []).map((req: any) => (
                    <RequestRow
                      key={req.id}
                      req={req}
                      onAccept={() => {
                        setTokenTarget({ id: req.id, mode: "accept" });
                      }}
                      onRefuse={() => {
                        setTokenTarget({ id: req.id, mode: "refuse" });
                      }}
                      onResend={() => handleAction(req.id, "resend")}
                      busy={action.isPending}
                    />
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Token 子对话框：接受/拒绝时输入邮件里的 token */}
      <Dialog open={!!tokenTarget} onOpenChange={(v) => !v && setTokenTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <KeyRound className="w-5 h-5" />
              输入邮件 Token
            </DialogTitle>
            <DialogDescription>
              将"{tokenTarget?.mode === "accept" ? "接受" : "拒绝"}"该变更请求。请粘贴 OVH 邮件中的确认 token。
            </DialogDescription>
          </DialogHeader>
          <Input value={token} onChange={(e) => setToken(e.target.value)} placeholder="邮件中的 token 字符串" />
          <DialogFooter>
            <Button variant="outline" onClick={() => setTokenTarget(null)}>
              取消
            </Button>
            <Button
              disabled={!token || action.isPending}
              onClick={() => tokenTarget && handleAction(tokenTarget.id, tokenTarget.mode, token)}
            >
              {action.isPending ? "提交中…" : tokenTarget?.mode === "accept" ? "确认接受" : "确认拒绝"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function ContactField({
  label,
  placeholder,
  value,
  onChange,
}: {
  label: string;
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <label className="text-[12px] font-semibold block mb-1.5">{label}</label>
      <Input placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

function RequestRow({
  req,
  onAccept,
  onRefuse,
  onResend,
  busy,
}: {
  req: any;
  onAccept: () => void;
  onRefuse: () => void;
  onResend: () => void;
  busy: boolean;
}) {
  // 旧前端字段：state / serviceDomain / fromAccount / toAccount / askingAccount / contactTypes(数组) / dateRequest / dateDone
  const state = String(req.state || "").toLowerCase();
  const tone =
    state === "done"
      ? "success"
      : state === "refused" || state === "cancelled"
        ? "default"
        : "warning";
  const types = Array.isArray(req.contactTypes) ? req.contactTypes.join(" / ") : "";
  const canAct = state === "todo" || state === "doing" || state === "validatingbycustomers";
  return (
    <div className="border border-border rounded-2xl p-3 space-y-1.5">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-mono font-semibold text-[12px]">#{req.id}</span>
        <Chip tone={tone}>{req.state || "—"}</Chip>
        {types && <span className="text-[11px] text-muted-foreground">{types}</span>}
      </div>
      {req.serviceDomain && (
        <p className="text-[12px] font-mono break-all">{req.serviceDomain}</p>
      )}
      {(req.fromAccount || req.toAccount) && (
        <p className="text-[11px] text-muted-foreground font-mono">
          {req.fromAccount || "—"} → {req.toAccount || "—"}
          {req.askingAccount && <span className="ml-2">· 发起人 {req.askingAccount}</span>}
        </p>
      )}
      <p className="text-[11px] text-muted-foreground">
        {req.dateRequest ? `请求 ${new Date(req.dateRequest).toLocaleString("zh-CN")}` : ""}
        {req.dateDone && <span className="ml-2">· 完成 {new Date(req.dateDone).toLocaleString("zh-CN")}</span>}
      </p>
      {canAct && (
        <div className="flex gap-1.5 pt-1">
          <Button size="sm" variant="outline" onClick={onAccept} disabled={busy}>
            <Check className="w-3.5 h-3.5 mr-1" />
            接受
          </Button>
          <Button size="sm" variant="outline" onClick={onRefuse} disabled={busy}>
            <XIcon className="w-3.5 h-3.5 mr-1" />
            拒绝
          </Button>
          <Button size="sm" variant="outline" onClick={onResend} disabled={busy}>
            <RefreshCw className="w-3.5 h-3.5 mr-1" />
            重发邮件
          </Button>
        </div>
      )}
    </div>
  );
}
