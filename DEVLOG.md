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
