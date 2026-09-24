import { useState } from "react";
import { Copy, Cpu, Network, Terminal } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { copyText } from "@/lib/clipboard";

const tools = [
  { name: "网络测试", command: "curl -sL yabs.sh | bash -s -- -fg", icon: Network },
  { name: "硬件检测", command: "curl -sL https://ba.sh/sick | bash -s -- -cn", icon: Cpu },
] as const;

export function DiagnosticTools() {
  const [copying, setCopying] = useState<string | null>(null);
  const [manual, setManual] = useState<(typeof tools)[number] | null>(null);

  async function copy(tool: (typeof tools)[number]) {
    setCopying(tool.name);
    try {
      await copyText(tool.command);
      toast.success(`${tool.name}命令已复制`, { description: "粘贴到服务器终端后运行" });
    } catch {
      setManual(tool);
    } finally {
      setCopying(null);
    }
  }

  return (
    <>
      <section
        aria-label="检测工具"
        className="flex flex-col gap-3 rounded-2xl border border-border bg-card p-3 sm:flex-row sm:items-center sm:justify-between sm:p-4"
      >
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground">
            <Terminal className="h-4 w-4" aria-hidden="true" />
          </div>
          <div>
            <h2 className="text-sm font-semibold">检测工具</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">点击复制命令，在服务器终端运行</p>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-2 sm:shrink-0">
          {tools.map((tool) => (
            <Button
              key={tool.name}
              type="button"
              variant="outline"
              className="h-11 gap-2 px-3 sm:px-4"
              aria-label={`${tool.name}，复制命令`}
              aria-busy={copying === tool.name}
              disabled={copying !== null}
              onClick={() => void copy(tool)}
            >
              <tool.icon aria-hidden="true" />
              {tool.name}
              <Copy className="text-muted-foreground" aria-hidden="true" />
            </Button>
          ))}
        </div>
      </section>

      <Dialog open={manual !== null} onOpenChange={(open) => { if (!open) setManual(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{manual?.name}命令</DialogTitle>
            <DialogDescription>浏览器未允许自动复制。请长按或选中下方命令复制，再粘贴到服务器终端运行。</DialogDescription>
          </DialogHeader>
          <textarea
            aria-label={`${manual?.name ?? "检测"}命令`}
            readOnly
            value={manual?.command ?? ""}
            rows={3}
            spellCheck={false}
            onFocus={(event) => event.currentTarget.select()}
            className="w-full resize-none rounded-xl border border-border bg-muted p-3 font-mono text-base leading-relaxed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </DialogContent>
      </Dialog>
    </>
  );
}
