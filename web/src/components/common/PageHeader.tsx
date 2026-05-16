import type { LucideIcon } from "lucide-react";

interface PageHeaderProps {
  icon: LucideIcon;
  title: string;
  description?: string;
  action?: React.ReactNode;
}

/**
 * 统一的页面顶部 header：
 * 左侧 icon 方块 + 标题 + 描述，右侧操作按钮
 * 所有页面都用这个，保持视觉一致
 */
export function PageHeader({ icon: Icon, title, description, action }: PageHeaderProps) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
      <div className="flex items-center gap-3 min-w-0">
        <div className="w-11 h-11 rounded-xl bg-secondary flex items-center justify-center flex-shrink-0">
          <Icon className="w-6 h-6 text-foreground" strokeWidth={1.75} />
        </div>
        <div className="min-w-0">
          <h1 className="text-2xl sm:text-[28px] font-bold text-foreground leading-tight tracking-tight">
            {title}
          </h1>
          {description && (
            <p className="text-[14px] text-muted-foreground mt-0.5 truncate">{description}</p>
          )}
        </div>
      </div>
      {action && <div className="flex items-center gap-2 flex-shrink-0">{action}</div>}
    </div>
  );
}
