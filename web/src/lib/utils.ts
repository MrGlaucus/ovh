import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/** 合并 className，去重 + Tailwind 冲突优先级处理。shadcn-ui 标配 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
