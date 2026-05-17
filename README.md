# OVH 控制台

OVH 独立服务器 / VPS / Eco 系列**抢购 + 监控 + 管理**控制台。

实时检测 OVH 各数据中心库存,发现可购买的服务器时按用户配置(机房、内存、存储、带宽、vRack)自动下单。后台同时管理已购买服务器的全生命周期(重启 / 重装 / IPMI / BIOS / 启动模式 / 维护工单 / 联系人变更 / 带宽 / 防火墙 / FTP 备份 / vRack / Secondary DNS 等)。

> 本项目基于 [coolci/OVH-BUY](https://github.com/coolci/OVH-BUY) 二开。
> 主要改造:Python Flask 后端 → Go (Gin) + SQLite;前端 Vue 重写为 Vite/React + TanStack + shadcn-ui;前后端 //go:embed 打包成**单二进制**(自带 SQLite, 跨平台无依赖);新增强制 OvhCredsGate / 配置绑定狙击 / 双 SQLite driver(modernc + mattn build tag 切换)等。

## 技术栈

| 层 | 技术 |
|---|---|
| 前端 | Vite 5 + React 18 + TypeScript + TanStack Router + TanStack Query + shadcn-ui + Tailwind + recharts |
| 后端 | Go 1.21+ + Gin + 官方 [go-ovh](https://github.com/ovh/go-ovh) SDK |
| 持久化 | SQLite(`modernc.org/sqlite` 纯 Go / `mattn/go-sqlite3` cgo 双 driver, build tag 自动选) |
| 通知 | Telegram Bot Webhook |
| 部署 | 单二进制(前端 //go:embed 进 Go 二进制) 或前后端分开跑 |

## 项目结构

```
.
├── server/   # Go 后端 (Gin, 默认 :19998)
│   ├── main.go
│   ├── webembed_ui.go    # build tag=ui 时把 web/ 整目录 embed 进二进制
│   ├── webembed_noui.go  # 默认 build,无前端
│   └── internal/
│       ├── app/          # State 聚合
│       ├── db/           # SQLite 层 (schema.sql + 各表 CRUD)
│       ├── handlers/     # Gin handler
│       ├── monitor/      # 服务器补货监控
│       ├── vps/          # VPS 补货监控
│       ├── sniper/       # 配置绑定狙击
│       ├── purchase/     # 下单流程
│       ├── price/        # OVH cart 询价
│       └── ...
└── web/      # 前端 (Vite + TanStack, dev 默认 :19997)
    └── src/
        ├── routes/       # 文件路由
        ├── components/   # 共享组件 + AuthGate / OvhCredsGate
        ├── hooks/        # TanStack Query hooks
        └── lib/          # 子公司表 / OVH 数据中心常量 / utils
```

后端详细文档见 [server/README.md](server/README.md)。

## 部署方式

### 方式 A:单二进制(推荐生产)

前端 build → Vite 输出到 `server/web/` → Go `-tags ui` 触发 `//go:embed` 把整目录嵌入二进制 → 单文件部署、双击即用。

```powershell
# Windows PowerShell
cd web
npm install
npm run build

cd ..\server
go build -tags ui -trimpath -ldflags "-s -w" -o ovh-server.exe
.\ovh-server.exe
```

Linux 一样,把 `.exe` 去掉。**不需要外部 SQLite 库,二进制自带。** 默认监听 `:19998`,浏览器打开 `http://localhost:19998` 即用,数据库自动建在 `./data/sniper.db`。

### 方式 B:开发(前后端分开跑)

```bash
# 后端
cd server
go run .                # 默认 :19998

# 前端(另一个终端)
cd web
npm install
npm run dev             # 默认 :19997, /api/* 自动反代到 19998
```

浏览器打开 `http://localhost:19997`。


## 首次访问

打开浏览器后会依次出现两层全屏遮罩,都过了才能进主界面:

1. **AuthGate**:输入 `.env` 里设置的 `API_SECRET_KEY`(没设的话用默认值,见下)
2. **OvhCredsGate**:填 OVH `APP KEY / APP SECRET / CONSUMER KEY`,选 OVH 子公司(Zone),`Endpoint` / `IAM` 自动派生。前端会调 `POST /api/verify-auth` 真去 OVH 验一次,通过才放行。

凭据通过后,前端立刻在后台 prefetch 三件套(服务器目录 / catalog / 可用性),用户切到服务器列表页**直接出数据,不会再走"加载中"**。

## 配置

`server/.env.example` → 复制成 `server/.env` 改:

```bash
API_SECRET_KEY=...               # 前端访问后端的密钥, 必须改
PORT=19998                       # 后端监听端口
LISTEN_HOST=                     # 空 = 所有网卡(IPv4+IPv6); 127.0.0.1 锁回环; 0.0.0.0 公网
ENABLE_API_KEY_AUTH=true         # 关掉的话所有 /api/* 不验证密钥, 仅本地调试用
GIN_MODE=release                 # debug | release
DEBUG=false                      # true 时启用 debug 日志
```

OVH 凭据**不放 env**,通过前端 OvhCredsGate / 设置页录入,落 SQLite `kv` 表的 `config` key。`.gitignore` 默认拒绝所有 `.env` 入库。

## 主要功能

### 抢购
- **服务器列表**:卡片网格 + 实时 DC 库存灯(绿可用 / 红缺货),点击直接选配置下单
- **配置选择器**:按 OVH `addonFamilies`(CPU / 内存 / 系统盘 / 数据盘 / 带宽 / vRack)分组单选,默认值预选
- **抢购队列**:每台服务器 × 每个 DC × 数量 独立任务,可暂停 / 恢复 / 删除,按 retry interval 轮询 OVH 库存
- **fail-fast**:用户选的配置匹配不上 OVH 当前可订购的 addon → 整单失败,绝不退化到默认 HDD
- **配置绑定狙击**:把"已下架"的型号 + 配置绑定监听,OVH 重新上架立即抢
- **价格预览**:18 个 OVH subsidiary 切换比价(EUR / USD / CAD / GBP / SGD / AUD / INR / PLN ...),前端用本地 catalog 算,不走 cart 流程

### 监控
- **服务器补货**:订阅 planCode + DC 组合,状态变化推 Telegram,可选自动下单
- **VPS 补货**:同上,针对 OVH VPS 产品线(区分 Linux / Windows 镜像)
- **历史时间线**:每个订阅完整变化记录

### 已购服务器管理
- **概览**:硬件信息 + 服务到期 + IP / 网卡 + MRTG 流量图
- **电源 / 系统**:重启 / 重装(含 ZFS / 软 RAID / 自定义分区)/ IPMI 控制台 / 启动模式 / SPLA Windows 解锁 / 任务列表 / BIOS / 安装进度。重装接口加了 per-service `TryLock`,防双击重复提交
- **维护**:维护记录 + 硬件更换工单(硬盘 / 内存 / 散热)+ 联系人变更(Token 邮件确认)
- **高级**(9 个 sub-tab):Burst / 防火墙 / Backup FTP / Secondary DNS / 虚拟 MAC / vRack / 可订购升级 / 附加选项 / IP 规格
- **隐私模式**:一键打码所有 IP / MAC / 反向 DNS 主机名

### 其它
- **账户管理**:余额 / 退款记录 / 邮件历史
- **抢购历史**:订单 + 价格 + 倒计时 + OVH 订单链接直跳
- **详细日志**:实时刷新,按级别 / 关键字筛选

## 持久化

全部业务数据在 SQLite(`data/sniper.db`),7 张表:

| 表 | 用途 |
|---|---|
| `kv` | 单例配置(OVH 凭据 / TG token / VPS 检查间隔等) |
| `queue` | 抢购队列 |
| `history` | 抢购历史 |
| `servers` | OVH 服务器目录缓存(刷新一次写一次,2h TTL) |
| `catalogs` | OVH 公共 catalog 每个 subsidiary 一份(2h TTL),浏览页价格走它 |
| `monitor_subscriptions` | 服务器补货订阅 |
| `vps_subscriptions` | VPS 补货订阅 |
| `config_sniper_tasks` | 配置绑定狙击任务 |

日志仍走 JSON 文件(`data/logs/app.log.json`),不进 SQLite。

## 缓存策略

| 数据 | 后端 TTL | 前端 staleTime | 后台轮询 | 触发刷新 |
|---|---|---|---|---|
| 服务器目录 | 2h(SQLite + 内存 ServerCache) | 2h | ❌ 完全访问触发 | 缓存过期时下一次访问 / 手动刷新按钮 |
| OVH catalog(价格) | 2h(SQLite `catalogs` 表) | 2h | ❌ | 同上 |
| 实时可用性 | — | 1 分钟 | ❌(原每 60 秒轮询已关) | 同上 |

启动时不主动调 OVH,只把 SQLite 现有数据加载到内存。`ServerCache` 用 SQLite 真实 `updated_at` 重建时间戳,旧数据不会被当成"刚刷过的"。

## 安全 / 鉴权

- 后端所有 `/api/*`(除少数白名单如 `/health` / `/telegram/webhook`)都要求 `X-API-Key` 请求头
- 两层全屏 gate:AuthGate(API 密钥) + OvhCredsGate(OVH 凭据)
- API Key 存浏览器 localStorage,失效自动清除并要求重新输入
- OVH 凭据落 SQLite kv 表,前端通过 OvhCredsGate / 设置页录入
- `.gitignore` 默认拒绝所有 `.env` 文件入库(只允许 `*.env.example`)

## OVH API 对接

下单流程严格对齐 OVH 官方 [order-cart-examples](https://github.com/ovh/order-cart-examples):

```
POST /order/cart                         → cartId
POST /order/cart/{id}/assign
POST /order/cart/{id}/eco                → itemId
POST /order/cart/{id}/item/{itemId}/configuration × 3  (datacenter / os / region)
POST /order/cart/{id}/eco/options × N
GET  /order/cart/{id}/summary
POST /order/cart/{id}/checkout
```

价格计算 = 基础 plan 月费 + 各 addon family 选中 addon 月费累加(`ovhjk/parser/price.go` 1:1 移植到前端 `web/src/hooks/use-availability.ts`)。

## 端口

| 服务 | 端口 |
|---|---|
| Go 后端(生产单二进制 / 开发) | **19998** |
| Vite dev server(仅开发) | 19997 |
| OVH Telegram webhook 入口 | `/api/telegram/webhook`(无需鉴权,IP 白名单) |
