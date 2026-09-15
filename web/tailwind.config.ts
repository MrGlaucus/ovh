import type { Config } from "tailwindcss";

/**
 * Tailwind 配置：
 * - darkMode: class（手动切换主题）
 * - 颜色、圆角、间距等通通走 CSS 变量（globals.css 定义），组件不硬编码 hex
 * - 全局禁用阴影（设计规范决定）
 */
export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    container: {
      center: true,
      padding: "1.5rem",
      screens: { "2xl": "1400px" },
    },
    extend: {
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
        success: "hsl(var(--success))",
        warning: "hsl(var(--warning))",
        // info 此前只有 globals.css 里的变量、没在这里映射，Chip/StatusDot 等
        // 满世界的 bg-info/text-info 类全都没生成 —— 蓝色系状态（付款核验中、
        // 交付中、延迟等）实际渲染出来是无色的。补上映射让设计意图生效。
        info: "hsl(var(--info))",
        "button-primary": {
          DEFAULT: "hsl(var(--button-primary))",
          foreground: "hsl(var(--button-primary-foreground))",
          hover: "hsl(var(--button-primary-hover))",
          active: "hsl(var(--button-primary-active))",
          disabled: "hsl(var(--button-primary-disabled))",
          "disabled-foreground": "hsl(var(--button-primary-disabled-foreground))",
        },
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
      boxShadow: {
        none: "none",
        DEFAULT: "none",
        sm: "none",
        md: "none",
        lg: "none",
        xl: "none",
      },
      fontFamily: {
        sans: ["Inter", "-apple-system", "BlinkMacSystemFont", "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "sans-serif"],
        mono: ["SF Mono", "Monaco", "Cascadia Code", "Roboto Mono", "Consolas", "monospace"],
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
} satisfies Config;
