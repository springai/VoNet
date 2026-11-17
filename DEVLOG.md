# DEVLOG

> **Wi-Fi Calling 回归基线：Commit `46a44bd`**
>
> 完整提交：`46a44bd3ea21d63ed17ade85ec3bdb70355173b1`
>
> 提交时间：`2026-11-16 14:52:13 +08:00`
>
> 提交标题：`feat: remove device count limits`
>
> 如果 Wi-Fi Calling、VoLTE/IMS 通话、短信或 IKE/IPSec 出现回归，请优先到 GitHub VoNet 项目的 `46a44bd` 提交中获取参考实现和对照差异。该版本的 Wi-Fi Calling 基础能力正常，包括拨打电话、接听/挂断和短信收发。

本文件同时维护在 `VoNet` 和 `VoNet-Agent` 仓库中。两个仓库中的内容应保持一致，用于记录本地新增架构、功能、Bug 修复和部署方式。同步上游时，不能用上游版本覆盖本文件，也不能未经核对删除本文列出的本地能力。

## 1. 架构拆分

### VoNet

VoNet 负责管理和业务控制面：

- Web 管理 UI。
- SQLite 数据库和迁移。
- 管理员认证、会话、CSRF、API Token、SSO。
- Agent 注册、通信密钥、证书签发、撤销和轮换。
- 设备、卡片、策略、代理、短信、通话和日志管理。
- Agent gRPC 服务端和远程命令调度。
- VoWiFi orchestrator、IKEv2/IPSec、IMS、SIP、RTP。
- 运营商兼容策略、代理节点、设备业务策略和持久化。

### VoNet-Agent

VoNet-Agent 负责本地硬件执行面：

- USB、QMI、MBIM、AT 和 PC/SC 硬件发现。
- AT/QMI/MBIM 命令执行。
- PC/SC reader 映射、SIM 身份读取、PIN、SIM session 和 EAP-AKA。
- 网络注册、蜂窝数据、短信、通话、USSD 等本地执行。
- 硬件快照、设备状态、命令结果和事件上报。
- Agent 本地 HTTPS 面板和入网配置。

VoNet 不再通过本地 QMI、AT、PC/SC 等接口自行发现设备。设备发现、状态和命令执行统一通过 VoNet-Agent gRPC 完成。

## 2. 通信与安全

- Agent 使用 `/agent/enroll` 自动入网。
- VoNet gRPC 使用独立端口 `17577` 和双向 mTLS。
- Agent Hello 中的 Agent ID 必须与客户端证书 SPIFFE Agent ID 一致。
- Agent 证书按 Agent ID 签发，旧证书在换发密钥或重新入网后失效。
- Agent 通信密钥是 URL-safe Base64 编码的 256 位随机密钥，UI 中生成后只显示一次。
- 换发通信密钥会撤销旧密钥和旧证书、断开 Agent，并要求使用新密钥重新入网。
- 换发密钥和删除 Agent 均有危险二次确认。
- VoNet 系统设置展示 HTTPS 证书 SHA-256 指纹，Agent 使用 `VONET_SERVER_CERT_SHA256` 固定校验。
- Agent 本地面板保留固定用户名 `admin`，强制 HTTPS、Argon2id、CSRF 和登录限速。
- 敏感卡片字段使用 AES-256-GCM；ICCID 保留明文用于查询，IMSI、MSISDN、GID、SMSC、EID/AID 和派生数据加密。

## 3. 设备管理

- 选中 Agent 后才显示刷新、重新扫描、添加设备和该 Agent 的设备列表。
- Agent 条目、设备工具栏、设备列表和设备详情组合在同一操作区域。
- 添加设备弹窗打开后立即扫描，并持续轮询 Agent 硬件快照。
- Agent 快照会重新执行硬件枚举，以发现刚插入但尚未缓存的设备。
- 已配置设备不会重复出现在待添加列表。
- 支持删除设备、添加设备、修改设备名称和配置。
- 删除设备只删除 VoNet 配置和运行时绑定，不删除已保存 SIM 卡。
- 设备操作通过 `AgentID + RemoteDeviceID` 路由到 Agent gRPC。
- 配置接口同时支持配置 ID 和远端硬件 ID，避免 `Reader-...-hash` 等配置页面为空。
- PC/SC 新设备建议 ID 在 USB 序列号为空时回退到 ReaderName。
- Agent 设备列表使用 VoNet 已保存的设备名称，而不是强制显示硬件名称。

## 4. Agent 管理

- 支持新建 Agent、编辑名称和备注、换发密钥、删除 Agent。
- 删除 Agent 会撤销其密钥和证书、断开连接，并清理该 Agent 的设备配置和 VoWiFi 运行时。
- 删除 Agent 不删除已保存 SIM 卡。
- Agent 列表显示在线状态、最后活动时间、版本和能力。
- Agent 离线时禁用远程写操作，危险命令断线后不自动重试。

## 5. VoWiFi、通话和短信

- VoNet 负责发起 VoWiFi、代理选择、IKE/IPSec、IMS、SIP 和 RTP。
- Agent 仅提供远程 SIM 身份、SIM session 和 AKA 数据。
- USB SIM reader 固定使用 PC/SC 和 VoWiFi-only 策略：
  - `airplane_enabled=true`
  - `network_enabled=false`
  - `vowifi_enabled=true`
  - `runtime_mode=pcsc`
- VoWiFi AKA 结果不重放；换卡、换 Reader 或切换 eSIM Profile 会立即失效相关缓存。
- 卡片保存时优先缓存稳定身份，VoWiFi 启动优先读取数据库，只在必要时执行 SIM session 和 AKA。
- 普通硬件快照不重复读取已知卡片身份。仅在首次发现、拔插/ATR 变化、启动校验或手动验证时读取身份。
- 拨号、通话、短信统一走 VoWiFi IMS；蜂窝设备仍可按策略使用 AT/QMI/MBIM。

### 已修复的关键通话问题

RemoteSIM 早期未完全复刻 PC/SCAdapter：

- 未按 `MNCLength` 和运营商 route 生成 Home PLMN。
- 未应用 `applyAssignedCarrierRoute`。
- IKE 身份缺少 PC/SC IMEI lease。
- `ReadIdentityWithoutRotation` 错误轮换 IMEI。

上述问题会导致运营商 TAS 在 `100 Trying` 后返回 `487 Request Terminated`，表现为“秒挂断”。修复后 RemoteSIM 必须：

- 使用 SIM `MNCLength` 计算 Home MCC/MNC。
- 应用运营商 route。
- 读取并按策略轮换 PC/SC IMEI lease。
- 在鉴权失败时按 PC/SCAdapter 的退避策略处理 IMEI。
- 保持 RemoteSIM 身份字段与本地 PCSCAdapter 一致。

## 6. 已修复的稳定性问题

- VoNet 启动不再调用本地设备发现或周期扫描。
- Agent 快照为空导致设备永久“恢复中”的问题已修复。
- Agent 10 秒心跳不再执行完整卡片读取。
- 添加设备不会因为只读取旧快照而找不到刚插入的硬件。
- 卡片候选状态在 Agent 模式下会正常加载，不再永久显示“待稳定确认”。
- 当前硬件快照正常响应时，历史操作错误不会把设备长期标为“不稳定”。
- RemoteSIM 卡片回写不再覆盖已保存的显示名、备注、运营商、MCC/MNC 和 PLMN。
- VoNet-Agent 正常扫描到未配置设备，实时刷新期间不会清空已选择的设备或表单。
- 从设备管理自动带入 ICCID 时，保存卡片的 ICCID 为只读。

## 7. 本地新增或重点修改的文件

以下路径在同步上游时必须重点保留。

### VoNet

- `internal/agentproto/`
- `internal/agentgrpc/`
- `internal/agentregistry/`
- `internal/server/agent_api.go`
- `internal/server/device_router.go`
- `internal/server/device_api.go`
- `internal/store/encryption.go`
- `internal/store/command_ledger.go`
- `internal/vowifi/remote_agent.go`
- `internal/pcsc/pin.go`
- `internal/device/pcsc_pin.go`
- `cmd/vonet/remote_card_profile.go`
- `web/src/components/agents/`
- `web/src/pages/DevicesPage.tsx`
- `deploy/vonet.service`
- `proto/agent/v1/agent.proto`
- `scripts/check-agent-contract.ps1`
- `scripts/generate-agent-proto.ps1`

### VoNet-Agent

- `cmd/vonet-agent/`
- `internal/agentproto/`
- `internal/connector/`
- `internal/agentconfig/`
- `internal/hardware/`
- `internal/enroll/`
- `internal/localauth/`
- `internal/localtls/`
- `internal/webui/`
- `deploy/vonet-agent.service`

## 8. 部署

### 构建

VoNet：

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o vonet-linux-amd64 ./cmd/vonet
```

VoNet-Agent：

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o vonet-agent-linux-amd64 ./cmd/vonet-agent
```

### 服务器

- VoNet 二进制：`/opt/vonet/bin/vonet`
- VoNet 数据：`/opt/vonet/data/vonet.db`
- VoNet-Agent 二进制：`/opt/vonet-agent/bin/vonet-agent`
- VoNet-Agent 数据：`/opt/vonet-agent/data`
- VoNet HTTP：`17575`
- VoNet-Agent gRPC：`17577`
- VoNet-Agent HTTPS：`17576`

服务：

- `vonet.service`
- `vonet-agent.service`

Agent 入网需要：

- VoNet 地址包含端口，例如 `https://192.168.1.3:17575`
- VoNet HTTPS 证书 SHA-256 指纹
- 一次性 Agent 通信密钥

## 9. 已验证的 Wi-Fi Calling 基础

2026-11-17 真实卡片和网络验证通过：

- 英国 O2：拨打、收短信正常。
- 英国 CTExcel/CTE：拨打、收短信正常。
- 英国 ASDA：拨打、收短信正常。
- 新西兰 2degrees：拨打、收短信正常。
- 美国 T-Mobile：拨打、收短信正常。

测试覆盖还包括：

- Agent mTLS 入网。
- Agent 离线重连。
- Agent 重启和 VoNet 重启恢复。
- 设备删除、添加和修改。
- PC/SC 多 reader 发现。
- VoWiFi IKE/IPSec、IMS、SIP、RTP。
- 通话接通、保持、DTMF 和挂断。
- 短信接收和发送。

## 10. 上游同步规则

同步 GitHub VoNet 或 VoNet-Agent 上游版本时：

1. 先读取并保留 `DEVLOG.md`。
2. 将 Commit `46a44bd` 作为 Wi-Fi Calling 的参考基线。
3. 不允许覆盖本文列出的新增文件。
4. 对共享文件使用三方合并，重点检查：
   - Agent proto 和生成代码。
   - mTLS、证书、通信密钥和撤销。
   - DeviceRouter 和远程设备操作。
   - RemoteSIMAdapter。
   - PC/SC IMEI lease、MNCLength 和 carrier route。
   - 设备发现、卡片候选和生命周期状态。
   - 通话、短信和 VoWiFi runtime。
5. 如果上游 Wi-Fi Calling 回归，对照 `46a44bd` 的呼叫、短信、IMS 和 PC/SC 实现。
6. 合并后必须运行：

```text
go test ./...
go vet ./...
npm test
npm run build
```

7. 必须至少验证：
   - Agent 入网和 mTLS。
   - 新硬件能够被发现并添加。
   - 设备配置页面不为空。
   - SIM 候选状态能够变为稳定。
   - 拨打电话能够接通并挂断。
   - 能够收发短信。

## 11. 后续维护

每次新增功能或修复 Bug 后，在本文件追加：

- 日期。
- 功能或 Bug 现象。
- 根因。
- 修复边界。
- 涉及文件。
- 验证方式和真实运营商结果。
- 对上游合并的风险提示。

## 12. 本地变更记录

### 2026-11-17 SIM 卡片概览运行状态重构

- 现象：VoWiFi 和蜂窝运行状态布局与新设计不一致，详情字段混在动画区域。
- 修复：VoWiFi 改为身份、安全隧道、鉴权、注册四阶段顺序动画；当前阶段显示旋转状态，只有阶段通过后才显示勾选。语音、短信和 USSD 分别显示可用、准备中或不可用。蜂窝状态改为 RSRP、RSRQ、SINR 仪表盘和信号质量视图，详情统一归入 `SIM / 设备`。
- 涉及文件：`web/src/components/devices/OverviewVowifiCard.tsx`、`web/src/components/devices/OverviewNetworkCard.tsx`、`web/src/components/devices/OverviewSimPanel.tsx`、`web/src/components/cards/CardOverviewTab.tsx`、`web/src/types.ts`、`internal/server/device_api.go`、`web/src/lib/i18n-en.ts`。
- 验证：`npm test`、`npm run build`、`go test ./internal/server` 通过，并已部署 VoNet。
- 合并风险：保留 `VoWiFiRuntime.attempt/sequence/startedAt` 字段，避免同步上游时丢失运行阶段和连续注册时长数据。

### 2026-11-17 SIM 卡片列表取消自动刷新

- 现象：SIM 管理的卡片列表约每 10 秒自动重新加载，列表视觉刷新频繁，影响浏览和操作。
- 根因：`CardsPage` 使用 `usePolling` 每 10 秒调用 `loadCards`，后台刷新会重新覆盖列表状态。
- 修复：移除卡片列表定时轮询，仅保留首次加载、手动刷新以及保存、删除、导入后的必要更新。
- 涉及文件：`web/src/pages/CardsPage.tsx`。
- 验证：`npm test` 84 项通过，`npm run build` 通过，并已部署 VoNet。
- 合并风险：同步上游时不要再加入 `CardsPage` 的固定 10 秒卡片列表轮询。

### 2026-11-17 全局 UI 按 VoHive 风格重构

- 现象：旧版标题、卡片、表格、表单和标签页风格偏硬，与参考站的柔和玻璃面板布局不一致。
- 修复：仅调整 UI 层，统一浅蓝灰画布、半透明圆角面板、蓝色主操作、状态胶囊、分段标签页、表格、表单、弹窗和底部 Dock。将 `IMEI 池`、`代理中心`、`导出代理` 移入设置菜单，设备菜单保留 `SIM 管理` 和 `设备管理`。
- 涉及文件：`web/src/reborn.css`、`web/src/components/shell/AuthenticatedShell.tsx`、`web/src/components/ui/`、`web/src/lib/i18n-en.ts`。
- 验证：13 个桌面页面和 6 个手机页面无水平溢出；`npm test`、`npm run build` 通过。
- 合并风险：不涉及 API、数据库、Agent proto 或业务逻辑；同步时保留 UI 设计变量和 Dock 分组。

### 2026-11-17 SIM 与设备工作区改为参考站双栏布局

- 现象：SIM 管理和设备管理的左列表信息过密，添加入口位于页面顶部，整体布局与参考站设备页不一致。
- 修复：仅调整 UI。左列改为约 238px 的卡片/设备列表，条目使用柔和圆角和紧凑状态布局；右侧承载详情。`添加卡片`、`添加设备` 移到左侧列表最后一项下方。设备 Agent 管理压缩为工作区顶部轻量栏，原有选择、编辑、换密钥、删除和设备操作逻辑不变。
- 涉及文件：`web/src/pages/CardsPage.tsx`、`web/src/pages/DevicesPage.tsx`、`web/src/components/devices/DeviceListPanel.tsx`、`web/src/components/devices/DeviceListItemCard.tsx`、`web/src/components/agents/AgentManager.tsx`、`web/src/reborn.css`。
- 验证：桌面左列 238px、右侧详情自适应，两个添加按钮均位于左列尾部；手机390px 视口无水平溢出；`npm test`、`npm run build` 通过。
- 合并风险：不修改卡片、设备、Agent 的状态机、API 或路由；同步时保留 `workspace-layout` 和 `workspace-list-*` 样式。

### 2026-11-17 SIM 概览运行状态拆分到顶部

- 分析结论：当前项目仅实现了 Quectel `+QCFG="ims"` 模组级开关和 `volteCapable` 能力标志，没有独立的蜂窝 VoLTE IMS 注册、SIP/RTP 会话或 VoLTE runtime。因此蜂窝模式不展示虚构的 VoLTE/IMS 注册动画。
- 修复：仅调整 UI。新增顶部独立运行区域：VoWiFi 类型显示 VoWiFi 进度、语音/短信/USSD 状态和蜂窝摘要；USB 读卡器蜂窝区域显示“不适用”；其他设备显示飞行模式、信号、蜂窝数据和 SIM PLMN。蜂窝类型标记为 VoLTE，只展示信号仪表和每秒采样的 60 秒实时流量动画，不展示未实现的 IMS 动画。短信、通话按钮位于运行区右上角；数据漫游、重启模组、重连 VoWiFi 汇总到概览底部的快捷操作区。顶部动画、标签栏、SIM / 设备、流量和快捷操作保持独立间距。
- 涉及文件：`web/src/components/cards/CardOverviewTab.tsx`、`web/src/components/devices/OverviewCellularStatusCard.tsx`、`web/src/components/devices/OverviewRealtimeTrafficCard.tsx`、`web/src/reborn.css`、`web/src/lib/i18n-en.ts`。
- 验证：VoWiFi 读卡器实际页面顶部显示 VoWiFi 进度和“蜂窝：不适用”；`npm test` 84 项、`npm run build` 通过。
- 合并风险：不修改 VoWiFi orchestrator、蜂窝通话通道、CellularIMS 配置或 Agent 逻辑；若未来实现独立 VoLTE IMS runtime，可在 `OverviewRealtimeTrafficCard` 同层增加 VoLTE 状态。

### 2026-11-17 Aurora Glass 外壳与底部单击导航

- 现象：页面背景、玻璃面板和导航交互不够统一，底部按钮存在二级弹出菜单，桌面侧栏与用户预期不符。
- 修复：仅调整 UI。加入宽幅柔和 Aurora 渐变背景和细网格，统一 Light/Dark 玻璃面板、标题、表格、表单、分段标签和过渡动画。桌面与移动端统一使用底部居中的 5 项 Dock，按钮单击直达分区首页，不再出现二级菜单；子页面通过内容区标签栏切换。
- 涉及文件：`web/src/components/shell/AuthenticatedShell.tsx`、`web/src/components/shell/WorkspaceSectionNav.tsx`、`web/src/components/ui/PageHeader.tsx`、`web/src/reborn.css`。
- 验证：桌面和手机均显示底部 Dock，侧栏隐藏，单击设备到 `/cards`、单击设置到 `/settings`，无二级菜单；`npm test`、`npm run build` 通过。
- 合并风险：不修改 API、数据库、Agent proto 或业务逻辑；同步时保留 `app-shell-frame`、`workspace-section-tabs` 和 `bottom-dock-*` 样式。

### 2026-11-17 SIM 配置与 AT/USSD 页面优化

- 现象：`卡片配置`、`设备配置` 缺少统一圆角内容容器；AT 和 USSD 被拆成两个标签，离线或不支持终端时仍以独立提示页代替输入区。
- 修复：仅调整 UI。两个配置页分别使用统一玻璃圆角容器；`AT 终端`、`USSD` 合并为 `AT/USSD` 标签，内部按参考布局拆成 AT 终端交互和 USSD 会话两块。AT 根据设备在线状态、AT 端口和读卡器类型判断可用性，USSD 根据卡片可操作状态判断可用性；不可用时输入框保留但禁用，悬停显示禁止光标。旧 `tab=at`、`tab=ussd` 路由兼容跳转到合并标签。
- 涉及文件：`web/src/pages/CardsPage.tsx`、`web/src/components/cards/CardAtUssdTab.tsx`、`web/src/components/devices/DeviceAtTab.tsx`、`web/src/components/devices/DeviceUssdTab.tsx`、`web/src/reborn.css`、`web/src/lib/i18n-en.ts`。
- 验证：`npm test` 84 项通过，`npm run build` 通过；Linux amd64 二进制已部署，`vonet.service` 为 `active`，Agent 已重新连接。
- 合并风险：不修改 AT/USSD API、会话状态机、设备能力判断来源或 Agent proto；同步时保留 `CardAtUssdTab`、`config-tab-surface`、`terminal-*` 和禁用输入状态。
