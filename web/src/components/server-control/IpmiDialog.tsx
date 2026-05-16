import { useEffect, useRef, useState } from "react";
import { Monitor, Loader2, ExternalLink, Download } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { toast } from "sonner";

/**
 * IPMI / KVM 控制台对话框：
 * - 打开后 20s 倒计时 + 调 /console
 * - kvmipHtml5URL / serialOverLanURL → 显示打开链接按钮
 * - kvmipJnlp → 自动下载 .jnlp 文件
 */
export function IpmiDialog({
  serviceName,
  open,
  onOpenChange,
}: {
  serviceName: string;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const [countdown, setCountdown] = useState(20);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<{ url?: string; accessType?: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const startedRef = useRef(false);

  useEffect(() => {
    if (!open) {
      // 重置
      setCountdown(20);
      setLoading(false);
      setResult(null);
      setError(null);
      startedRef.current = false;
      return;
    }
    if (startedRef.current) return;
    startedRef.current = true;

    // 倒计时
    setLoading(true);
    setCountdown(20);
    const interval = setInterval(() => {
      setCountdown((p) => (p <= 1 ? 0 : p - 1));
    }, 1000);

    // 调接口
    (async () => {
      try {
        const res = await api.get(`/server-control/${serviceName}/console`);
        clearInterval(interval);
        setLoading(false);
        const value = res.data?.console?.value;
        const accessType = res.data?.accessType;
        if (!value) {
          setError("控制台返回为空");
          return;
        }
        if (accessType === "kvmipJnlp") {
          // 下载 JNLP
          const blob = new Blob([value], { type: "application/x-java-jnlp-file" });
          const url = window.URL.createObjectURL(blob);
          const a = document.createElement("a");
          a.href = url;
          a.download = `ipmi-${serviceName}.jnlp`;
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          window.URL.revokeObjectURL(url);
          toast.success("JNLP 文件已下载，请用 Java 打开");
          setResult({ accessType });
        } else {
          // URL（kvmipHtml5URL / serialOverLanURL）
          setResult({ url: value, accessType });
        }
      } catch (e: any) {
        clearInterval(interval);
        setLoading(false);
        setError(e?.response?.data?.error || e?.message || "请求失败");
      }
    })();

    return () => clearInterval(interval);
  }, [open, serviceName]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Monitor className="w-5 h-5" />
            IPMI / KVM 控制台
          </DialogTitle>
          <DialogDescription>
            正在向 OVH 申请远程控制台访问。请耐心等待倒计时结束。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col items-center justify-center py-6 gap-3">
          {loading ? (
            <>
              <Loader2 className="w-12 h-12 animate-spin text-muted-foreground" />
              <p className="text-[13px] text-muted-foreground">
                正在获取控制台访问… {countdown > 0 && `约 ${countdown}s`}
              </p>
            </>
          ) : error ? (
            <p className="text-[13px] text-destructive">{error}</p>
          ) : result ? (
            <div className="w-full space-y-3">
              <p className="text-[13px] text-success font-semibold text-center">控制台访问已就绪</p>
              {result.url ? (
                <a
                  href={result.url}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center justify-center gap-2 w-full px-4 py-3 border border-border rounded-2xl hover:bg-secondary/50 transition-colors text-[13px] font-semibold"
                >
                  <ExternalLink className="w-4 h-4" />
                  在新标签页打开控制台
                </a>
              ) : (
                <div className="flex items-center justify-center gap-2 w-full px-4 py-3 border border-border rounded-2xl text-[13px] text-muted-foreground">
                  <Download className="w-4 h-4" />
                  JNLP 文件已下载，请用 Java 打开
                </div>
              )}
              <p className="text-[11px] text-muted-foreground text-center">
                链接仅当次有效。访问类型：{result.accessType}
              </p>
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
