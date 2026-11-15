# VoNet 本地修改记录

本文档记录基于上游 VoNet-reference 项目的所有本地修改和新增内容，
方便后续同步上游版本时进行分析，防止覆盖本地更改。

基准版本：4da2cbc (fix(device): recover EC20 after cold boot and USB reset)

---

### 58. SSO Cookie 传输策略与 schema 35 同步

**修改内容**:

- 同步 VoCat-reference `384a447..70b0e69` 中 SSO 浏览器入口、Cookie 传输策略、IPv6 公开地址和文档增量；VoNet 保留 migration 33 API Token、migration 34 SSO 表和全部本地业务表。
- VoNet schema 35 只为 `sessions` 增加 `secure_cookies INTEGER NOT NULL DEFAULT 0 CHECK (secure_cookies IN (0,1))`，逐会话记录登录入口已确认的 Secure Cookie 传输策略。旧 Session 和 API Token 原样保留，不根据数据库备份猜测其传输策略，也不允许把已有策略自动降级。
- `Login` 和 `CreateSession` 使用显式 `secureCookies` 参数；`CreateSession` 在事务内读取管理员 credential epoch 并写入 Session，避免与密码轮换并发时提交迟到凭据。
- 直接 TLS、受信任 TLS 反代和同源 HTTPS 浏览器提示只会加强 Cookie 保护。`VONET_SECURE_COOKIES=true` 会强制 Secure Cookie，因此纯 HTTP 的普通私网浏览器或 API 客户端可能无法保存/回传 Cookie；HTTP 开发入口应改用 HTTPS，或仅在明确接受该限制时强制开启。
- 反代必须保留浏览器 `Host`，只向受信任代理开放并发送单一明确的 `X-Forwarded-Proto: https`。系统不从 `X-Forwarded-Host` 推导入口，忽略未受信任、重复或含多个协议值的转发头。
- Session/CSRF Cookie 保持 host-only；不同 HTTP 私网、IPv6、域名或 TLS 入口分别建立会话，不通过 Domain 共享。SSO 流 Cookie 仍只覆盖 `/api/auth/sso`，API Token 与浏览器入口相互独立。
- SSO 公开地址按浏览器表示规范化 IPv6（包括压缩形式和 IPv4-mapped IPv6），不同 IPv6 主机或端口仍拒绝；Generic OAuth2 的纯 HTTP 后端只有在所有 DNS 地址持续为私网且不指向特权/元数据地址时才允许。
- CI/release/docker smoke 增加 schema 34 到 35、`auth-cleanup`、TLS 反代、HTTP 私网、IPv6、`docker compose config --quiet` 和容器内 CLI 验证。构建顺序固定为先生成 `web/dist`，再执行 Go 构建，确保 `go:embed` 包含最新前端。

**auth-cleanup 保留语义**:

- `vonet auth-cleanup --database ...` 正常打开数据库时保持现有迁移语义，会把 schema 34 快照升级到 schema 35，并撤销恢复的 Session/API Token 和 pending SSO flow、推进 credential epoch。
- `vonet auth-cleanup --database ... --no-migrate` 只打开已有的 SQLite 数据库，不执行 schema 迁移；它用于恢复 schema 34 快照后的凭据清理，完成清理后 `user_version` 仍为 34。
- SSO provider 配置与已绑定外部身份继续保留，不把撤销凭据误当成解绑或删除 SSO 配置。
- 恢复数据库后必须在开放服务前执行清理；密码、Session/API Token、SSO secret 和数据库内容不得写入日志或 smoke 输出。

**验证结果**:

- `cmd/vonet` bootstrap/auth-cleanup 测试适配新签名，并保留 Session/API Token/pending flow 撤销、epoch 推进和 SSO binding/config 保存断言。
- 全量 Go 测试覆盖 `store/auth/server/sso` 的 schema 34 到 35、TLS Cookie、HTTP 私网和 IPv6 向量。
- release 使用真实 `vonet auth-cleanup --database` 二进制 smoke；Docker amd64/arm64 镜像各自执行容器内 smoke。
- `docker compose -f docker-compose.sso-test.yml config --quiet` 和 workflow 静态检查纳入验证。

**备份与回滚**:

- schema 35 无自动降级。升级前停止写入并使用 SQLite backup API 或 `.backup` 生成一致快照；手工冷备份必须把主数据库、`-wal`、`-shm` 作为同一组复制，同时备份 TLS、插件和 `/etc/vonet/env`。
- 回滚必须恢复与旧二进制匹配的完整快照。只恢复主数据库而保留较新的 WAL/SHM 可能重放 schema 35 写入或产生混合状态，禁止这种组合。
- 恢复 schema 34 快照后若要启动旧二进制，必须使用匹配的旧二进制执行 `auth-cleanup`，或使用新二进制执行 `vonet auth-cleanup --database /opt/vonet/data/vonet.db --no-migrate`。该步骤撤销恢复凭据但保留 SSO 配置和外部绑定，且不会升级 schema。
- 禁止先用会执行迁移的新二进制运行普通 `auth-cleanup` 把 schema 34 快照升级到 schema 35，再启动旧二进制。新二进制的普通 `auth-cleanup` 只用于 schema 35 快照或明确接受升级到 schema 35 的恢复流程。
- 发布 smoke 必须确认 Compose 文件可解析、镜像内 CLI 可创建并清理独立数据库，且最终二进制在 `web/dist` 构建完成后生成。

---

## 本地修改列表

### 1. 读卡器设备强制使用 HardwareKind 判断（而非 DeviceType）

提交: b5a8d73, 049147c
文件: cmd/vonet/main.go

将多处 store.DeviceTypeUSBSIMReader 判断改为 pcsc.HardwareKind 判断，
使设备类型识别基于实际硬件而非数据库存储值。

涉及函数：
- configureDeviceBackends - SIM PIN 配置
- restoreDefaultCellularRadios - 恢复默认蜂窝射频
- disableAllDeveloperCellularData - 开发者模式禁用蜂窝数据
- configureVoWiFiRuntime - VoWiFi 适配器选择逻辑

### 2. 设备 HardwareKind 变更时重置状态

提交: 339ab6c
文件: internal/device/manager.go, cmd/vonet/main.go

- 新增 OnHardwareKindChange 回调和 SetOnHardwareKindChange 方法
- 当设备硬件类型变更时（如 EC20 换成读卡器），重置后端、eSIM 传输、快照等状态
- 在 main.go 中注册回调，调用 vowifiManager.Reset(deviceID)

### 3. 读卡器类型增加随机 IMEI

提交: eee06c5
文件: internal/vowifi/pcsc_adapter.go, internal/device/manager.go, cmd/vonet/main.go

- 定义6个预设 IMEI 池
- PCSCAdapter 新增 imeiMap 字段跟踪每个设备的当前 IMEI
- rotateIMEI() 每次 VoWiFi 尝试/重试时随机选择不同的 IMEI
- CurrentIMEI() 供设备管理器查询当前 IMEI
- 设备管理器新增 PCSCIMEIProvider 和 SetPCSCIMEIProvider
- refreshCardReader() 中填充快照 IMEI 字段

### 4. 增加系统诊断功能

提交: 25b0cab
文件: internal/server/general_api.go, web/src/pages/DiagnosticsPage.tsx, web/src/App.tsx, web/src/components/shell/AuthenticatedShell.tsx

- 新增 XFRM 和 PC/SC 环境检测 API
- 新增前端诊断页面

### 5. 修复开发者模式 UI 丢失问题

提交: f0e56c4
文件: web/src/pages/DevicesPage.tsx, web/src/pages/SettingsPage.tsx, web/src/components/devices/DeviceAddDialog.tsx

### 6. 增加 WiFi Calling 失败原因日志

提交: dfd155f
文件: internal/vowifi/ike/provider.go, internal/vowifi/runtime/manager.go

- EAP 循环中添加 rejectFatalNotifications 检查
- 错误信息中附加 ePDG 返回的实际 payload 类型列表

### 7. 修复读卡器走 AT PORT 的问题（两处修复）

提交: d5b117b
文件: cmd/vonet/main.go

configureDeviceBackends 中 PCSC 设备跳过了 SetBackend 调用，导致 backend 为空，
VoWiFi 运行时尝试使用 EC20 适配器（AT 命令）而非 PCSC 适配器。
修复1：configureDeviceBackends 中显式调用 SetBackend。修复2：VoWiFi 工厂函数使用 HardwareKindFor 直接判断硬件类型。

### 8. README 更新

文件: README.md

### 9. SOCKS5 导出代理增加 UDP ASSOCIATE 支持

提交: b5d15d4
文件: internal/exportproxy/proxy_socks.go

上游代理仅支持 TCP CONNECT（command 1），VoWiFi 的 IKEv2/IPsec 需要 UDP 传输，
客户端请求 UDP ASSOCIATE 时返回 code 7（command not supported）。

修改内容：
- serveSOCKS 重构为 switch 分发：command 1 → CONNECT，command 3 → UDP ASSOCIATE
- serveSOCKSConnect：提取原有 CONNECT 逻辑，行为不变
- serveSOCKSUDPAssociate：新 handler，读取客户端地址，打开随机 UDP 端口监听，
  返回 relay 地址，启动 relay 协程，阻塞在 TCP 控制连接上（TCP 断开即关闭 relay）
- udpRelay 结构体 + serve()/handlePacket()：接收 SOCKS5 UDP 数据报，解析头部，
  通过模块的 bound dialer 转发 payload 到目标，将响应中继回客户端
- parseUDPHeader：解析 SOCKS5 UDP 数据报（ATYP 1/3/4）提取目标主机、端口和 payload 偏移
- buildUDPReply：构造 SOCKS5 UDP 响应数据报
- writeUDPAssociateReply：写入 UDP ASSOCIATE 成功应答（code 0）及 relay 地址

关键设计：
- 所有出站 UDP 通过 dialer.DialContext("udp", ...) 走模块蜂窝接口
- TCP 控制连接在 UDP 会话生命周期内保持打开（标准 SOCKS5 行为）
- sync.Once 防止 UDP listener 双重关闭
- atomic.Pointer 跟踪首个数据报的客户端地址

---

## 上游版本同步记录

| 上游版本 | 本地同步状态 | 说明 |
|---------|------------|------|
| 4da2cbc | 已同步 | 基准版本 |
| 56512e6 | 已同步 | fix(sms): accept slot zero and skip empty notifications |
| 96a1233 | 已同步 | fix(device): recover EC20 from prolonged network search |
| e7bbdef | 已同步 | fix(vowifi): preserve fatal IKE notifications during EAP |
| 152da09 | 已同步 | FIX #120: MT SMS RP-ACK returns 403 with wrong IMS identity |
| aa00c32 | 已同步 | Update README |
| 4760e33 | 已同步 | fix(workflows): update action versions and add retry script |
| fbe2119 | 已同步 | fix(vowifi): avoid single-fragment SKF payloads |
| 7dd4746 | 已同步 | fix(ims): 修复短信收发中的注册和身份使用问题 |
| b8f4c0c | 已同步 | fix(device): allow SMS storage index zero |
| 8f18f45 | 已同步 | fix(sms): clear modem storage after a successful sync |
| fd14902 | 已同步 | Fix/ims ipsec mss - MTU兼容模式 |
| fcb84ee | 已同步 | Update Star History links and image sources |
| 9592c74 | 已同步 | fix(esim): align RetrieveNotificationsList with OpenEUICC's encoding |
| d83e757 | 已同步 | FIX #127 |
| 2a2765d | 已同步 | FIX #129 |
| 21e51ad | 已同步 | feat: implement notification integrations, auth service, settings API, and core runtime modules |
| 621be85 | 已同步 | feat: implement authentication service and settings API |

---

### 10. 修复 EAP-AKA AUTH_FAILED 间歇性失败

提交: 3103e02
文件: internal/vowifi/runtime/manager.go, internal/vowifi/pcsc_adapter.go, internal/vowifi/orchestrator.go, internal/vowifi/types.go

问题: 读卡器类型 WiFi Calling 间歇性出现 "responder reported fatal notification 7" (AUTH_FAILED)，
过一段时间自动恢复。根因是快速重试导致 SIM 卡 SQN 与 HSS 失步。

修复内容:
- 增大重试初始间隔: defaultRetryInitial 从 2s 增大到 10s，defaultRetryMaximum 从 30s 增大到 60s
- EAP 认证失败时使用更长重试间隔: eap_authentication_rejected 时初始间隔 30s
- AUTH_FAILED 后保持 IMEI 不变: PCSCAdapter 新增 authFailed 标记，ReadIdentity 在标记存在时
  不轮换 IMEI，避免加剧 HSS SQN 失步
- 隧道建立成功后清除 authFailed 标记，允许下次正常轮换 IMEI
- IKE_SA 失败时的 INFORMATIONAL delete 已由上游 cleanupPendingIKE 机制覆盖（无需额外修改）

涉及函数:
- runtime/manager.go: scheduleAutoRetryLocked - 根据错误类型选择初始重试间隔
- pcsc_adapter.go: MarkAuthFailed/MarkAuthSuccess - 标记认证失败/成功状态
- pcsc_adapter.go: ReadIdentity - 检查标记决定是否轮换 IMEI
- orchestrator.go: Enable - 隧道失败时调用 MarkAuthFailed，成功时调用 MarkAuthSuccess

### 11. 错开多设备 VoWiFi 启动时间避免 HSS 过载

提交: 3103e02
文件: internal/vowifi/runtime/manager.go

问题: 5个读卡器同时启动 VoWiFi 时，所有设备同时发起 EAP-AKA 认证，
导致 ePDG/HSS 过载，所有设备同时出现 notification 7 (AUTH_FAILED)。
指数退避的重试时间窗口高度重叠，每次重试仍然同时冲击 HSS。

修复内容:
- eentry 结构体新增 firstEnablePending 标记，跟踪是否为首次启用
- runOperations 在首次启用前插入随机延迟（0-30s），基于 deviceID 的 FNV-1a 哈希
- 延迟是确定性的：同一设备每次启动延迟相同，不同设备延迟不同
- 仅首次启用延迟，重试和重连不受影响
- 延迟期间可被 manager 关闭信号中断
- 重试退避中加入设备特定抖动（0-10s），避免多设备重试时间完全重叠
- staggerJitter(deviceID, max) 生成与 staggerDelay 不同的哈希偏移

涉及函数:
- staggerDelay(deviceID) - 基于 FNV-1a 哈希生成确定性首次启动延迟
- staggerJitter(deviceID, max) - 基于 FNV-1a 哈希生成确定性重试抖动
- runOperations - 首次启用前检查标记并插入延迟
- scheduleAutoRetryLocked - 重试退避中加入设备特定抖动

---


### 12. 修复 RTP 媒体路由缺失导致通话10秒断开和无声音问题

提交: 04aceca
文件: internal/vowifi/ike/userspace_linux.go

问题: WiFi Calling 通话每到10秒自动断开，双方听不见声音。
日志显示 "[RTP] silence send error: ims: send RTP: write udp ... sendto: no route to host"。

根因: VoNet 只为 P-CSCF 地址安装了 TUN 路由，RTP 目标（IMS 媒体服务器）不在路由表中，
导致 RTP 包无法通过 IPsec 隧道发送。运营商检测到无媒体流后10秒自动断开通话。

修复内容:
- 替换 "unreachable default" 路由为全覆盖路由: 0.0.0.0/1 + 128.0.0.0/1 (IPv4), ::/1 + 8000::/1 (IPv6)
- 新增 installLANBypassPolicy 函数，确保非隧道流量（如 VoNet Web UI）仍走 LAN 网关
- 新增 findDefaultGateway 和 findLANAddress 辅助函数，运行时发现默认网关和 LAN 地址
- LAN bypass 使用策略路由: 从 LAN 地址发出的流量走 LAN 表，从隧道地址发出的流量走 TUN

涉及函数:
- configureFamily - 替换 unreachable 路由为全覆盖路由
- configure - 添加 LAN bypass 策略调用
- installLANBypassPolicy - 安装 LAN bypass 策略路由
- findDefaultGateway - 发现默认网关
- findLANAddress - 发现主机 LAN 地址


### 13. 修复通话中滋滋声/电流声问题
提交: 04aceca

**文件**: `internal/vowifi/ims/rtp_media.go`

**问题**: 通话时出现滋滋声/电流声，对方听不清楚。原因是 `startSilenceSender` 每 200ms 无条件发送静音 RTP 包，即使浏览器正在通过 WebSocket 发送真实音频。两者共享同一个 `pending` 缓冲区和序列号/时间戳计数器，导致 RTP 流中静音包和真实音频包交替出现，接收端产生音频伪影。

**修复方案**:
- 在 `rtpMedia` 结构体中新增 `lastRealAudioWrite time.Time` 字段
- 新增常量 `silenceSuppressWindow = 400 * time.Millisecond`
- `WritePCM`（公开方法，供浏览器音频调用）在写入前更新 `lastRealAudioWrite = time.Now()`
- 新增 `writePCMInternal` 内部方法，不更新 `lastRealAudioWrite`
- `startSilenceSender` 在发送前检查 `time.Since(media.lastRealAudioWrite) < silenceSuppressWindow`，若最近有真实音频写入则跳过本次静音包发送
- 静音发送器使用 `writePCMInternal` 而非 `WritePCM`，避免污染真实音频检测

涉及新增:
- `lastRealAudioWrite` 字段
- `silenceSuppressWindow` 常量
- `writePCMInternal` 方法
### 14. 修复 AUTH_FAILED 后 IMEI 永不轮换导致持续失败
提交: 04aceca


**文件**: `internal/vowifi/pcsc_adapter.go`

**问题**: 当 EAP-AKA 认证失败（fatal notification 7）后，`MarkAuthFailed` 将 `authFailed` 标记为 true，此后 `ReadIdentity` 不再轮换 IMEI。如果该 IMEI 的 SQN 与 HSS 永久失同步，设备会一直用同一个 IMEI 重试，永远无法成功。

**修复方案**:
- 新增 `consecutiveFails map[string]int` 跟踪每个设备的连续失败次数
- 新增常量 `pcscAuthFailRotateThreshold = 3`
- `MarkAuthFailed` 中递增 `consecutiveFails`
- `MarkAuthSuccess` 中重置 `consecutiveFails` 为 0
- `ReadIdentity` 中判断：若 `authFailed` 为 true 且连续失败次数 < 3，保持 IMEI 不变（给 SQN 同步机会）；若 >= 3 次，强制轮换到新 IMEI

涉及新增:
- `consecutiveFails` 字段
- `pcscAuthFailRotateThreshold` 常量
### 15. 修复通话滋滋声根因：AMR-WB 编解码器不匹配
提交: c9a2276

**文件**: internal/vowifi/ims/rtp_media.go, web/src/components/call/CallPanel.tsx

**问题**: 通话中出现滋滋声/噪音，对方听不清。根因是远端 SDP 中 AMR-WB（payload 104）排在 PCMA（payload 8）前面，导致协商选择了 AMR-WB。但编码/解码函数只支持 PCMA 和 PCMU，用 PCMU 解码 AMR-WB 数据产生噪音。同时浏览器 AudioContext 使用 sampleRate: 16000，但 RTP 编解码器期望 8kHz，采样率不匹配加剧问题。

**修复方案**:
- 从 SDP offer 中移除 AMR-WB 和 AMR，只提供 PCMA(8)、PCMU(0)、telephone-event(100)
- configureRemote 中跳过 AMR-WB/AMR 编解码器
- 浏览器 AudioContext 改为原生采样率，JS 端显式线性插值重采样（上行原生→8kHz，下行8kHz→原生）
- 修复麦克风反馈：processor.connect(audioCtx.destination) 改为通过 muteGain（gain=0）连接
- 新增 playDtmfTone 函数，按下 DTMF 键时播放本地双音多频音调

涉及修改:
- internal/vowifi/ims/rtp_media.go：SDP offer/answer 移除 AMR-WB/AMR，configureRemote 跳过 AMR
- web/src/components/call/CallPanel.tsx：AudioContext 原生采样率 + 重采样 + muteGain + DTMF 音调

### 16. 修复外呼双方无声音问题
提交: c9a2276


**文件**: web/src/components/call/CallPanel.tsx, internal/vowifi/ims/rtp_media.go

**问题**: 外呼接通后双方都听不到声音。原因是 startAudio（连接浏览器音频 WebSocket）只在 handleAnswer（接听来电）中调用，外呼接通后 phase 转为 "connected" 但未调用 startAudio，浏览器从未建立音频连接。此外 	ransport 状态异步更新导致条件判断失败。

**修复方案**:
- phase 转为 "connected" 时，对外呼也调用 startAudio(active.id)
- 移除 	ransport === "vowifi" 条件检查（React 状态异步更新导致时序问题）
- startSilenceSender 立即发送第一个静音包（不等200ms定时器），帮助 NAT 穿越
-
eceive() 接受 telephone-event(payload 100) 包但跳过处理，避免误报 mismatch

涉及修改:
- web/src/components/call/CallPanel.tsx：connected 状态下调用 startAudio
- internal/vowifi/ims/rtp_media.go：立即发送静音包 + 接受 telephone-event 包
### 17. 修复 PCSC 读卡器 IMEI 为空导致无法拨打电话
提交: 6b5eee8

**文件**: internal/vowifi/pcsc_adapter.go

**问题**: Reader-02 设备（CTEXCEL 运营商）IMEI 为空，无法拨打电话（SIP 487 Request Terminated）。原因是 ReadIdentity 中
otateIMEI 在 dapter.resolve 和 dapter.service.ReadIdentity 之后调用。如果 SIM 卡读取失败（如 AUTH_FAILED 后重试），函数提前返回，IMEI 永远不会被分配。

**修复方案**:
- 将 IMEI 分配（
  otateIMEI）移到 ReadIdentity 函数最前面
- 在任何可能失败的操作之前先分配 IMEI
- 即使后续 SIM 读取失败，返回的 SIMIdentity 也包含已分配的 IMEI

涉及修改:
- internal/vowifi/pcsc_adapter.go：ReadIdentity 中 IMEI 分配提前
## 注意事项

- 同步上游版本时，注意 cmd/vonet/main.go 文件的合并，本地有多处修改
- internal/device/manager.go 有本地新增的 PCSCIMEIProvider 和 OnHardwareKindChange 相关代码
- internal/vowifi/pcsc_adapter.go 有本地新增的 IMEI 轮换逻辑和 authFailed 标记
- internal/vowifi/ike/provider.go 有本地新增的 EAP 错误诊断代码
- internal/exportproxy/proxy_socks.go 有本地新增的 UDP ASSOCIATE 支持（上游仅 TCP CONNECT）
- internal/vowifi/runtime/manager.go 有本地修改的重试间隔（10s/60s）、EAP 错误特殊处理和首次启动错开延迟
- internal/vowifi/orchestrator.go 有本地新增的 MarkAuthFailed/MarkAuthSuccess 调用
- internal/vowifi/ims/rtp_media.go 有本地新增的 lastRealAudioWrite、silenceSuppressWindow 和 writePCMInternal（静音抑制）
- internal/vowifi/pcsc_adapter.go 有本地新增的 consecutiveFails 和 pcscAuthFailRotateThreshold（IMEI 轮换阈值）
- internal/vowifi/ims/rtp_media.go 中 SDP 已移除 AMR-WB/AMR，只提供 PCMA/PCMU
- web/src/components/call/CallPanel.tsx 有本地修改的音频重采样、muteGain、DTMF 音调、外呼 startAudio
- internal/vowifi/pcsc_adapter.go 中 ReadIdentity 的 IMEI 分配已提前到函数开头





### 18. 修复 ReadIdentity 死锁导致 WiFi Calling 卡在 SIM 阶段
提交: 6b5eee8

**文件**: internal/vowifi/pcsc_adapter.go

**问题**: 修复 #17 后，WiFi Calling 启动时卡在 SIM 阶段，无错误无进展。根因是 ReadIdentity 中 dapter.mu.Lock() 后缺少 defer adapter.mu.Unlock()，所有 return 路径都未释放互斥锁，导致第二次调用时死锁。

**修复方案**:
- 在 dapter.mu.Lock() 后添加 defer adapter.mu.Unlock()

涉及修改:
- internal/vowifi/pcsc_adapter.go：ReadIdentity 中添加 defer Unlock

### 19. 修复 Reader-02 IMEI 为空导致无法拨打电话
提交: 6b5eee8

**文件**: internal/vowifi/pcsc_adapter.go

**问题**: 设备管理页面 Reader-02 的 IMEI 为空，且无法拨打电话（SIP 487 Request Terminated）。根因是设备 ID 不匹配：
- VoWiFi 运行时使用数据库设备 ID（如 "Reader-02"）调用 ReadIdentity，IMEI 存储在 imeiMap["Reader-02"]
- 设备管理器使用硬件设备 ID（如 "reader-3e6633c2c9509656"）调用 CurrentIMEI，查找 imeiMap["reader-3e6633c2c9509656"] → 空

**修复方案**:
- 在 ReadIdentity 中，resolve 拿到 selector 后，用 selector.USBPath（或 ReaderName）计算硬件设备 ID
- 将 IMEI 同时存储在 imeiMap 的数据库 ID 和硬件 ID 两个 key 下
- 硬件 ID 计算逻辑与 pcsc.DeviceID 一致（sha256 前8字节 hex）

涉及修改:
- internal/vowifi/pcsc_adapter.go：ReadIdentity 中双写 IMEI（数据库 ID + 硬件 ID）
- 新增 import：crypto/sha256, encoding/hex

### 20. CTEXCEL 运营商配置增加 contact_format: gsma
提交: 6b5eee8

**文件**: internal/vowifi/carrier_profiles.json

**问题**: Reader-02（CTEXCEL 运营商）IMEI 已正确分配，但外呼仍被 SIP 487 Request Terminated 拒绝。根因是 CTEXCEL 运营商配置文件缺少 contact_format: gsma，导致 SIP REGISTER 和 INVITE 的 Contact header 中 +sip.instance 使用随机 UUID 而非 IMEI（urn:gsma:imei:IMEI-0），运营商因无法识别设备而拒绝呼叫。

**修复方案**:
- 在 ipcc-ctexcel-23433 的 ims 配置中添加 register_options.contact_format: gsma
- 与 giffgaff 等其他使用 GSMA 格式的运营商保持一致

涉及修改:
- internal/vowifi/carrier_profiles.json：CTEXCEL profile 添加 contact_format

### 21. CTEXCEL SMS 不可用修复
提交: 6b5eee8

**文件**: internal/vowifi/carrier_profiles.json

**问题**: 添加 contact_format: gsma 后，CTEXCEL 的 IMS 注册成功但 SMS 不可用（ims_registered_sms_unavailable）。根因是 GSMA 格式的 Contact header 不会自动包含 +g.3gpp.smsip feature tag，需要通过 contact_extra_tags 显式添加。

**修复方案**:
- 在 CTEXCEL 的 register_options 中添加 contact_extra_tags: ["+g.3gpp.smsip"]
- 与 giffgaff 等运营商的配置保持一致

涉及修改:
- internal/vowifi/carrier_profiles.json：CTEXCEL profile 添加 contact_extra_tags

### 22. 修复 VoWiFi 通话中 DTMF 按键对方收不到
提交: 2a46efc

**文件**: internal/vowifi/ims/rtp_media.go, internal/vowifi/ims/call_runtime.go, internal/vowifi/types.go, internal/vowifi/orchestrator.go, internal/vowifi/runtime/manager.go, internal/server/server.go, internal/server/call_api.go

**问题**: VoWiFi 通话中按键盘数字，对方收不到 DTMF 信号。根因是 VoWiFi 路径完全没有实现 DTMF 发送功能。call_api.go 中 DTMF 只走 AT 命令路径（AT+VTS），VoWiFi 的 switch 中没有 dtmf case。

**修复方案**:
- rtp_media.go：新增 SendDTMF 方法，实现 RFC 2833 telephone-event RTP 包发送（payload type 100），发送3个 start 包 +3个 end 包
- call_runtime.go：新增 Session.SendDTMF，查找活跃通话并通过 media 发送 DTMF
- types.go：CallController 接口新增 SendDTMF 方法
- orchestrator.go：新增 Orchestrator.SendDTMF
- runtime/manager.go：新增 Manager.SendDTMF
- server.go：VoWiFiCallController 接口新增 SendDTMF
- call_api.go：VoWiFi switch 中新增 dtmf case，将 digit 变量提升到更广作用域

涉及修改:
- 7个文件，新增完整的 DTMF 发送链路

### 23. 优化 WiFi Calling 稳定性（参考 vohive 流程）
提交: 1d32212
**文件**: internal/vowifi/orchestrator.go, internal/vowifi/types.go, internal/vowifi/runtime/manager.go, cmd/vonet/main.go

**问题**: WiFi Calling 经常出现 EAP-AKA AUTH_FAILED（notification 7），过段时间又恢复。根因是 PCSC 读卡器的 SIM 操作（ReadIdentity + CheckReady + ReadSMSCenter）每次都需要获取 service 全局锁，多设备竞争时卡容易不稳定，导致 SQN 与 HSS 失步。

**参考 vohive 的做法**:
- vohive 在 SIM 就绪后有明确的稳定窗口，不会立即开始 IKE
- vohive 的 EAP-AKA 失败重试间隔更长，给 SQN 同步留出时间
- vohive 的启动错开更充分，避免多设备同时竞争

**修复方案**:
1. orchestrator.go：SIM ready 后增加可配置的稳定窗口（默认2秒），让 UICC 从连续 APDU 命令中恢复
2. types.go：Options 新增 SIMStabilizeDelay 字段
3. runtime/manager.go：
    - defaultRetryInitial 从10s 增加到15s
    - defaultRetryMaximum 从60s 增加到90s
    - defaultStaggerMaximum 从30s 增加到45s
    - EAP-AKA 失败重试间隔从30s 增加到45s
4. cmd/vonet/main.go：生产环境设置 SIMStabilizeDelay=2s

涉及修改:
- 4个文件，优化 WiFi Calling 启动流程和重试策略

### 24. 修复 Tunnel ready 后重新从 SIM 阶段开始的 bug
提交: 1d32212
**文件**: cmd/vonet/main.go

**问题**: 设备达到 Tunnel ready 状态后，会重新从 SIM 阶段开始 WiFi Calling。根因是2秒 SIM 稳定窗口期间，reconciliation 循环（每5秒运行一次）检测到状态不匹配，调用 RequestReconnect，导致 Enable 完成后立即又开始新的 Reconnect 操作。

时序问题：
1. Enable 开始 → SIM ready → SIM 稳定窗口（2秒）→ ePDG → Tunnel ready → IMS ready
2. 在 SIM 稳定窗口期间，reconciliation 循环触发 RequestReconnect
3. RequestReconnect 设置 reconnectPending = true（因为设备正忙）
4. Enable 成功后，runOperations 检测到 reconnectPending，立即开始新的 Reconnect

**修复方案**:
1. SIMStabilizeDelay 从2秒减少到500ms，缩小 reconciliation 干扰窗口
2. reconciliation 循环中 ICCID 不匹配触发 reconnect 时，增加 phase 检查：只在设备处于稳定状态（idle/failed/ims_ready/sms_ready）时才触发，避免在连接过程中干扰

涉及修改:
- cmd/vonet/main.go：减少 SIM 稳定延迟 + 增加 phase 检查

### 25. 修复 Tunnel ready 后重新开始的 bug（第二轮）
提交: 1d32212
**文件**: cmd/vonet/main.go

**问题**: 修复 #24 后问题依旧存在。根因是 phase 检查不够严格——允许了 ims_ready 和 sms_ready 状态下的 reconciliation reconnect。当第一个 Enable 成功达到 ims_ready 后，reconciliation 循环检测到 ICCID 不匹配，触发了 reconnect。

**修复方案**:
- phase 检查从 (idle/failed/ims_ready/sms_ready) 收紧为 (idle/failed)
- 只在设备完全空闲或明确失败时才允许 reconciliation 触发 reconnect
- 修复了两处 RequestReconnect 调用的代码结构（缺少闭合括号）

涉及修改:
- cmd/vonet/main.go：收紧 phase 检查 + 修复代码结构

### 26. 修复 automatic_tasks 无条件触发重连导致 Tunnel ready 后重新开始
提交: 1d32212
**文件**: internal/server/automatic_tasks.go, cmd/vonet/main.go

**问题**: 修复 #24 和 #25 后问题依旧存在。设备首次达到 Tunnel ready 后，约10秒后重新从 SIM 阶段开始，期间无任何错误日志。

**根因**: utomatic_tasks.go 中有三处 RequestReconnect 调用完全没有任何 phase 检查：
- line 335：任务执行前检查设备状态时
- line 552：恢复任务环境（VoWiFi enabled policy）时
- line 575：恢复飞行模式后恢复 VoWiFi 时

部署后时序：
1. 设备 Enable → SIM ready → ePDG → Tunnel ready → IMS ready ✓
2. 自动任务恢复函数执行，调用
   estoreAutomaticTaskEnvironment
3.
estoreAutomaticTaskEnvironment 中 RequestReconnect 无条件触发
4. 设备被迫重新从 SIM 阶段开始

**修复方案**:
1. automatic_tasks.go 三处 RequestReconnect 都增加 phase 检查：只在 state.Phase == "idle" || state.Phase == "failed" 时才触发重连
2. main.go line 1645（设备重新出现触发 reconnect）也增加相同 phase 检查
3. 已在连接过程中（sim_ready/access_ready/tunnel_ready/ims_ready/sms_ready）的设备不会被意外中断

涉及修改:
- internal/server/automatic_tasks.go：三处 RequestReconnect 增加 phase 检查
- cmd/vonet/main.go：设备重新出现 reconnect 增加 phase 检查
### 27. 优化设备通话页面手机端显示（重新设计）
提交: 3ead9d5

**文件**: web/src/pages/CallPage.tsx, web/src/components/call/CallPanel.tsx, web/src/vonet.css

**问题**: 设备通话页面在电脑端是三栏布局（设备选择 + 通话记录 + 拨号键盘），手机端只显示第三栏（拨号键盘），设备选择和通话记录完全隐藏，无法正常使用。此外手机端通话无声音（双方听不见），因为音频 WebSocket 连接未建立。

**修复方案**:

桌面端（≥980px）：保持原有三栏布局不变

手机端（<980px）全新设计：
- 顶部：设备横向滚动胶囊选择器（chip），显示设备名和在线状态
- 中部：搜索框 + 通话记录列表（可滚动）
- 底部：拨号显示区 + 退格键 + 绿色拨号按钮（固定在底部，适配 safe-area）
- 点击拨号区 → 弹出全屏拨号键盘覆盖层（带返回按钮，滑入动画）
- 点击通话记录号码 → 自动填入并弹出拨号键盘
- 拨号/接听/通话中 → 全屏深色通话界面，带挂断、静音、DTMF 键盘按钮
- 来电 → 全屏来电界面，带拒接/接听按钮

音频管理：
- 在 CallPage.tsx 中直接实现 startMobileAudio/stopMobileAudio
- 复制 CallPanel 的完整音频逻辑：connectCallMedia WebSocket + getUserMedia 麦克风
- AudioContext + ScriptProcessorNode 处理音频编解码（8kHz PCM16 重采样）
- 通话连接时自动建立音频，挂断/卸载时自动清理

涉及修改:
- web/src/components/call/CallPanel.tsx：新增 onPhaseChange prop（通知父组件通话状态）
- web/src/pages/CallPage.tsx：完全重写，桌面端保留三栏，手机端实现新设计 + 音频管理
- web/src/vonet.css：新增手机端样式（设备胶囊、搜索栏、拨号栏、键盘覆盖层、通话界面、深色模式适配）

### 28. 修复 PCSC 读卡器可重复添加及离线后重新出现在添加列表的问题
提交: d0ac641
**文件**: internal/server/device_api.go

**问题**: 同一个 PCSC 读卡器设备添加成功后，会显示离线，然后在"添加设备"对话框中再次出现，导致用户可以重复添加同一物理设备（如 Reader-01、Reader-02 指向同一个读卡器）。

**根因分析**:
1. physicalMatchesConfig 中 IMEI 比对是早返回逻辑（
   eturn config.ModemIMEI == entry.Snapshot.IMEI），而 PCSC 读卡器使用随机 IMEI（本地修改 #3），每次 VoWiFi 重试都会轮转。当 IMEI 不匹配时直接返回 false，后面的 ReaderName 稳定标识比对根本执行不到。
2. handleDiscoveredDevices 端点将已配置的设备仍然返回给前端（仅标记 configured: true），前端过滤不可靠时已添加设备会重新出现在添加列表中。
3. POST 添加接口缺少物理设备级别的防重复校验。

**修复方案**（三处改动）:

1. **physicalMatchesConfig — PCSC ReaderName 检查优先**:
    - 将 PCSC 的 ControlDevice ↔ ReaderName 匹配从 IMEI 检查之后移到之前
    - 确保 PCSC 设备通过稳定的 ReaderName 匹配，不受随机 IMEI 轮转影响

2. **POST /devices — 添加前物理设备防重复校验**:
    - indDiscoveredDevice 成功后，遍历已有配置调用 physicalMatchesConfig 检查
    - 若物理设备已存在配置，返回 409 Conflict device_already_configured

3. **GET /devices/discovered — 过滤已配置设备**:
    - 已通过 physicalMatchesConfig 匹配到配置的设备直接 continue 跳过
    - 不再返回给前端，从根源上杜绝已添加设备再次出现在添加对话框中

涉及修改:
- internal/server/device_api.go：physicalMatchesConfig 调整 PCSC 匹配顺序、handleDevices POST 增加防重复检查、handleDiscoveredDevices 过滤已配置设备
### 29. 修复读卡器类型偶尔走基站信号的问题（强制 PC/SC）
提交: 13c23a4
**文件**: internal/vowifi/integration/at.go, cmd/vonet/main.go

**问题**: PCSC 读卡器类型设备偶尔会走基站（蜂窝网络）信号，而不是始终走 PC/SC 通道。

**根因分析**:
ATMapper.resolve 通过 physicalMatchScore 函数匹配物理设备，但原函数只检查 IMEI、ATPort、QMIControl、USBPath 四种标识，完全不识别 PCSC 读卡器的 ReaderName。当读卡器的随机 IMEI 轮转后（本地修改 #3），匹配得分降为 0 或极低，导致：
1. configureDeviceBackends 无法解析读卡器 → 后端未设置为 "pcsc"
2.
econcileCardPolicies 跳过读卡器 → VoWiFi 策略未正确执行
3. 读卡器可能在没有飞行模式保护的情况下运行，出现走基站信号的情况

**修复方案**:

1. **physicalMatchScore 增加 PCSC ReaderName 匹配**（at.go）:
    - 新增 candidate.HardwareKind == pcsc.HardwareKind 时的 ControlDevice ↔ ReaderName 匹配
    - 得分 300（与 AT/QMI 控制路径匹配相同），确保 ATMapper 能正确解析读卡器设备
    - 使 configureDeviceBackends 和
      econcileCardPolicies 能正确管理读卡器

2. **
   econcileCardPolicies 增加 PCSC 兜底保护**（main.go）:
    - 当 mapper 无法解析读卡器设备时，强制设置 VoWiFiEnabled = true + NetworkEnabled = false
    - 杜绝读卡器在任何情况下被配置为走蜂窝网络

涉及修改:
- internal/vowifi/integration/at.go：physicalMatchScore 增加 PCSC ReaderName 匹配
- cmd/vonet/main.go：reconcileCardPolicies 增加 PCSC 设备 VoWiFi 强制保护
### 30. 修复同类型非读卡器设备共用同一配置的 bug（410WiFi棒/大疆4G/移远芯片）
提交: fa3af85
**文件**: internal/server/device_api.go, internal/vowifi/integration/at.go

**问题**: 当两个同类型设备（如两个大疆4G模块）同时接入时，第二个设备不会出现在添加列表中。当移除第一个设备后，第一个设备的配置会错误地使用第二个设备的数据。根因是 physicalMatchesConfig 和 physicalMatchScore 在 IMEI 快照未填充时（新发现设备），退化到 USBPath/ATPort 等弱匹配，无法可靠区分同类型的不同物理设备。此外，PCSC 读卡器的虚假 IMEI 可能与真实设备的固定 IMEI 碰撞，导致跨类型误匹配。

**修复方案**:

1. **illConfigFromPhysical — 持久化 USB 硬件标识**（device_api.go）:
    - 添加设备时，将 SerialNumber、VendorID、ProductID 存入 config.Extra JSON
    - 这些标识在发现阶段就可用，不依赖异步填充的 IMEI 快照

2. **physicalMatchesConfig — 增加硬件标识匹配**（device_api.go）:
    - SerialNumber 不匹配 → 直接拒绝（两个不同物理设备的 USB 序列号一定不同）
    - VendorID/ProductID 不匹配 → 直接拒绝（不同类型的设备不可能是同一个）
    - SerialNumber + VendorID 匹配 → 直接接受（强证据）
    - PCSC 跨类型保护：当 candidate 是 PCSC 但 config 不是（或反之），跳过 IMEI 比较（虚假 IMEI 碰撞是巧合）

3. **physicalMatchScore — 增加相同的硬件标识评分**（at.go）:
    - SerialNumber/VendorID/ProductID 不匹配 → 返回 0 分（直接排除）
    - SerialNumber + VendorID 匹配 → 得分 15000（高于 IMEI 的 10000）
    - PCSC 跨类型时跳过 IMEI 评分

涉及修改:
- internal/server/device_api.go：fillConfigFromPhysical 存储硬件标识、新增 extraHardwareIdentity 辅助类型、physicalMatchesConfig 增加匹配逻辑
- internal/vowifi/integration/at.go：新增 extraHWIdentity 辅助类型、physicalMatchScore 增加匹配逻辑


---

### 31. 修复同一USBPath下不同读卡器共用同一设备 + 设备ID改用纯USB序列号
提交: e72c823
**文件**: internal/pcsc/types.go, internal/pcsc/backend_linux.go, internal/pcsc/service.go, internal/pcsc/service_test.go, internal/device/manager.go, internal/server/device_api.go, internal/vowifi/integration/at.go, cmd/vonet/main.go

**问题**:
1. 两个不同读卡器（如Reader-TEST000000020100和Reader-TEST000000030100）具有相同USBPath时，插入一个设备后两个设备都同时恢复和在线
2. 读卡器设备ID使用序列号+接口号（如Reader-TEST000000020100），但接口号在换USB口后会变化（变为0000），导致同一物理读卡器被识别为不同设备

**根因分析**:
1. **PCSC层 (matchReader)**: 函数优先匹配USBPath，导致两个不同读卡器共享相同USBPath时被错误匹配到同一个物理读卡器
2. **ATMapper层 (physicalMatchScore)**: PCSC设备即使ReaderName不匹配，USBPath匹配仍得分+100，导致mapper.Get返回错误的物理设备
3. **Server层 (physicalForConfig)**: s.devices.Get(config.ID)直接返回设备，不验证PCSC设备的ReaderName是否匹配
4. **接口号不稳定**: 读卡器的ReaderName包含接口号（如TEST000000020100），但接口号在换USB口后会变化（变为TEST000000020000），导致同一物理读卡器无法匹配

**修复方案**:

1. **新增USBSerial字段** (types.go, backend_linux.go): Reader和Selector新增USBSerial字段，独立存储USB序列号（不含接口号），新增parseUSBSerial()函数
2. **DeviceID改用USBSerial** (service.go): 设备ID格式改为 `Reader-{USBSerial}`（如Reader-TEST00000002），不再包含接口号
3. **matchReader优先级重排** (service.go): 匹配优先级改为 ReaderName精确 > USBSerial > Name > USBPath（USBPath仅在无name时作为兜底）
4. **physicalMatchScore PCSC匹配** (at.go): PCSC设备优先匹配USBSerial（300分），回退到ReaderName（250分），都不匹配返回0分
5. **physicalForConfig验证** (device_api.go): Get匹配成功后，额外验证PCSC设备的SerialNumber或ReaderName是否与ControlDevice一致
6. **physicalMatchesConfig USBSerial匹配** (device_api.go): PCSC设备匹配改为ControlDevice对比Candidate.SerialNumber，回退到ReaderName和Extra中的serial
7. **fillConfigFromPhysical** (device_api.go): ControlDevice改为存储USBSerial（原来存ReaderName含接口号）
8. **PCSC binding resolver** (main.go): Selector创建时从Extra中读取usb_serial_number传入USBSerial字段
9. **物理发现传递USBSerial** (manager.go): PCSC reader发现时，将USBSerial传递为Candidate.SerialNumber
10. **统一匹配辅助函数** (main.go): 新增matchPCSCDevice()和pcscReaderSerial()，configureDeviceBackends()匹配和更新都改用USBSerial
11. **updatePCSCDeviceUSBPath冲突检测** (device_api.go): 两个设备共享USBPath时，根据ReaderName判断是否同一物理设备，离线设备清空USBPath，在线冲突标记冲突状态

**注意**: 之前用 `Reader-{Serial+Interface}` 格式添加的设备需要删除数据库后重新部署。

涉及修改:
- internal/pcsc/types.go：Reader和Selector结构体新增USBSerial字段
- internal/pcsc/backend_linux.go：新增parseUSBSerial函数，reader创建时设置USBSerial
- internal/pcsc/service.go：DeviceID改用USBSerial；matchReader完全重写，优先级ReaderName>USBSerial>Name>USBPath
- internal/pcsc/service_test.go：新增TestDeviceIDUsesUSBSerial测试
- internal/device/manager.go：PCSC candidate创建时传递SerialNumber=USBSerial
- internal/server/device_api.go：suggestedDeviceID改用USBSerial；fillConfigFromPhysical存储USBSerial到ControlDevice；physicalForConfig/physicalMatchesConfig改用USBSerial匹配
- internal/vowifi/integration/at.go：physicalMatchScore改用USBSerial匹配
- cmd/vonet/main.go：新增matchPCSCDevice/pcscReaderSerial辅助函数；configureDeviceBackends改用USBSerial；PCSC binding resolver传递USBSerial



### 32. 大疆设备ID改用"Baiwang-IMEI" + 修复同一设备共用多个设备配置的bug
提交: c78bf26
**文件**: internal/modem/discovery.go, internal/device/manager.go, internal/device/identity_qmi.go,
internal/server/device_api.go, internal/device/registration_native_qmi.go, cmd/vonet/main.go

**问题**:
1. 大疆设备ID使用USB路径（如Baiwang-1-2-2-1），插到不同USB口后ID变化，无法唯一标识设备
2. 两个大疆设备共享同一USBPath时，插入一个设备后两个设备都同时恢复和在线
3. DJI4G设备缺少物理存在检查（PCSC有但DJI没有），离线设备仍显示在线/恢复中状态
4. VoWiFi运行时状态覆写bug：离线DJI设备的vowifi_active被旧存储运行时覆盖为true

**修复方案**:

1. **设备ID改用IMEI** (discovery.go, identity_qmi.go, manager.go, main.go):
   - candidateID()中大疆设备无serial时返回空字符串，不再生成USB路径ID
   - manager.Discover()中新增QMI DMS充实：通过/dev/cdc-wdmX读取IMEI，ID设为"Baiwang-{IMEI}"
   - QMI IMEI缓存（djiIMEICache）：IMEI是固定硬件标识，只读取一次，后续周期性发现使用缓存，避免USB总线重置
   - provisioning阶段使用QMI DMS读取IMEI，ControlDevice存IMEI，前缀"Baiwang-"
   - nativeQMIControlMatches接受"Baiwang-"前缀匹配

2. **DJI设备物理存在检查** (device_api.go - configuredDeviceSummary):
   - 为DJI4G设备增加physicalForConfig检查（与PCSC同逻辑）
   - 设备离线时：network_phase="offline"，清空modem_phase/maintenance_phase，network_connected/data_connected=false

3. **离线DJI设备VoWiFi状态正确覆写** (device_api.go):
   - 覆写时更新runtimeResponse局部变量为idleVoWiFiRuntime（之前只更新result但没更新runtimeResponse）
   - 后续vowifi_active计算使用idle值（enabled=false, tunnel_ready=false），不再被旧存储运行时覆盖

4. **离线DJI设备清空USBPath** (device_api.go - deviceSummaries):
   - 设备离线且USBPath非空时，清空USBPath并写入数据库
   - 防止离线设备的旧USBPath误匹配到插入同一USB口的其他设备

5. **USBPath冲突解决** (device_api.go - updateDJI4GDeviceUSBPath):
   - 两个DJI设备都被检测为在线且共享同一USBPath时：
     - 调用physicalForConfig获取物理设备条目
     - 通过physicalEntry.ID == deviceID判断目标设备是否是端口真正所有者
     - 目标不是所有者→跳过USBPath更新（return）
     - 目标是所有者→清空另一个设备的USBPath并写入数据库
   - 核心逻辑：设备ID（Baiwang-IMEI）是唯一的，通过设备ID匹配确定端口所有权

**涉及修改**:
- internal/modem/discovery.go：candidateID()大疆设备无serial时返回""
- internal/device/manager.go：新增djiIMEICache字段和初始化；Discover()中QMI DMS充实+缓存逻辑
- internal/device/identity_qmi.go：ReadQMIIMEI公开方法（30秒超时）
- internal/device/registration_native_qmi.go：nativeQMIControlMatches接受"Baiwang-"前缀
- internal/server/device_api.go：configuredDeviceSummary增加DJI物理存在检查；VoWiFi覆写更新runtimeResponse；deviceSummaries离线DJI清空USBPath；updateDJI4GDeviceUSBPath基于设备ID的冲突解决；physicalMatchesConfig移除DJI的USBPath回退
- cmd/vonet/main.go：provisioning使用QMI DMS读取IMEI，ControlDevice存IMEI，前缀"Baiwang-"

### 33. 修复DJI设备因ModemIMEI为空导致USBPath误匹配的bug
提交: 7040295
**问题**:
physicalMatchesConfig 中通用 IMEI 检查（L2390）要求 config.ModemIMEI 和
entry.Snapshot.IMEI 都非空才生效。当设备刚连接、快照尚未生成时，entry.Snapshot
为 nil，检查被跳过，直接走到 USBPath 匹配，导致两个不同 DJI 设备（不同 IMEI）
因共享同一 USBPath 被误判为同一设备，两个设备配置同时在线。

**修复** (internal/server/device_api.go - physicalMatchesConfig):
- 在通用 IMEI 检查和 USBPath 匹配之间，为 DJI 设备增加 ControlDevice 回退匹配
- DJI 设备的 ControlDevice 存储的是 IMEI（provisioning 时设置），始终非空
- 候选设备的 SerialNumber 由 QMI DMS 充实设置
- 匹配逻辑：ControlDevice == candidate.SerialNumber → 同一设备 → return true
- 不匹配 → return false（阻断 USBPath 匹配路径）
- 确保 DJI 设备永远不会通过 USBPath 匹配，只通过 IMEI 匹配

### 34. 节点转换启动优化与状态同步修复
提交: 62b72ce
文件: internal/nodeadapter/service.go, cmd/vonet/main.go

**问题描述**:
1. 点击恢复按钮后，节点状态仍显示 error，但进程实际已在运行。原因是 handleProcessExit 在后台自动重启了进程，但 ResumeAll 发现 IsRunning=true 时直接跳过，未修正数据库中的 status 字段。
2. ResumeAll 未重置自动重启计数器，导致已达到30次重试上限的节点即使手动恢复也无法再被自动重启机制拉起。
3. 启动顺序不合理：VoWiFi/wificalling 模块先于 sing-box 节点适配器启动，但 wificalling 依赖 sing-box 提供的 SOCKS5 代理节点。

**修复内容**:

- ResumeAll 增加状态同步逻辑：当 IsRunning=true 但数据库 status 不是 "running" 时，主动将 status 更新为 "running"
- ResumeAll 在重启每个节点前清除其自动重启计数器（`restartInfo`），确保手动恢复后 handleProcessExit 能从零开始重试
- `StartEnabled` 增加重试机制：每个节点启动失败最多重试30次（`maxStartupRetries`），每次间隔2秒（`startupRetryDelay`），单个节点达到上限后跳过继续下一个
- 调整 cmd/vonet/main.go 启动顺序：将 `nodeAdapterSvc` 创建和 `StartEnabled` 移至 `configureVoWiFiRuntime` 之前执行，确保 sing-box SOCKS5 代理在 VoWiFi/wificalling 启动前就绪
### 35. HTTP代理转换功能与节点转换全面优化
提交: c5e4337
文件: internal/nodeadapter/store.go, internal/nodeadapter/service.go, internal/server/node_adapter_api.go, internal/singbox/config.go, internal/proxy/probe.go, web/src/pages/NodeAdaptersPage.tsx

**新增功能**:
- 节点转换页面新增「转为HTTP」按钮，可将Reality/VMess/Trojan等节点转换为本地HTTP代理
- HTTP代理与SOCKS5代理完全独立，各自有独立的启用状态、端口配置、进程管理
- 新增HTTP连通性检测功能（紫色检测按钮）
- 添加/编辑节点表单新增「HTTP本地设置」区块（监听IP、监听端口），与「SOCKS5本地设置」同级
- 表格新增「本地HTTP」列，显示HTTP代理的IP:端口和检测状态

**数据库迁移**:
- schemaV3: 新增 http_listen_host、http_listen_port、http_enabled 列
- schemaV4: 新增 http_status、http_last_error 列（独立跟踪HTTP代理状态）

**后端修复**:
- service.go: 新增 ConvertToHTTP、RemoveHTTP、CheckHTTP、startHTTPInstance 函数
- service.go: handleProcessExit 支持 -http 进程ID，分别更新SOCKS5/HTTP状态
- service.go: ResumeAll/StartEnabled 同时处理SOCKS5和HTTP实例的恢复与启动
- service.go: ConvertToHTTP 在端口为0时自动分配可用端口（从60000起）
- service.go: Create 创建节点时自动分配HTTP端口（默认从60000开始）
- service.go: Update 编辑节点时保留已有的HTTP端口/状态，避免被清零
- 新增 DefaultHTTPStartPort = 60000 常量和 nextHTTPPort() 端口分配函数
- Upsert 查询修复：VALUES占位符从17个修正为22个，与列数完全匹配

**前端修复**:
- SOCKS5/HTTP按钮完全独立：SOCKS5使用a.enabled，HTTP使用a.httpEnabled
- saveAdapter payload 新增 http_listen_host、http_listen_port 字段
- openImport/openCreate 时 HTTP监听IP 默认取全局设置IP
- HTTP监听端口默认值从0改为60000

### 36. 编辑节点进程重启修复 + 日志安全优化
提交: 04f9e3e
文件: internal/singbox/manager.go, internal/nodeadapter/service.go, internal/nodeadapter/store.go

**修复问题**:
- 编辑节点后，HTTP代理出现tcp_unreachable，因为Update函数只重启SOCKS5而不重启HTTP
- Manager的goroutine竞态条件：stopLocked杀死旧进程并从map中删除后，旧进程的exit goroutine
  读取到的是新实例，导致wasStopped=false，错误触发handleProcessExit标记状态为error
- 启动时sing-box config完整JSON输出到日志，包含服务器IP、端口、UUID、公钥等敏感信息
- [DEBUG-LIST]和[DEBUG-UPSERT]调试日志混入生产输出

**Manager竞态修复** (manager.go):
- Start方法改为捕获实例指针(curInst)而非在goroutine中通过map查找
- goroutine通过curInst.stopped读取停止状态，即使新实例已替换旧实例也能正确读取
- 通过identity比较(current == curInst)判断是否需要清理，防止误删新实例

**Update函数修复** (service.go):
- 编辑节点时先停止旧的SOCKS5和HTTP进程，再用新配置重启
- a.Enabled/a.HTTPEnabled仅在对应configured为true时才保留existing值
  (修复了!Socks5Configured时a.Enabled=false被existing.Enabled覆盖的bug)
- 重启SOCKS5条件: a.Enabled && a.Socks5Configured && a.ListenPort > 0
- 重启HTTP条件: a.HTTPEnabled && a.HTTPConfigured && a.HTTPListenPort > 0
- 未配置或端口为0的协议不会尝试重启，避免资源浪费

**日志清理** (manager.go, store.go):
- 移除启动时sing-box config完整JSON输出，改为仅记录config_len（配置长度）
- 移除所有[DEBUG-LIST]和[DEBUG-UPSERT]的fmt.Printf调试日志

### 37. PCSC读卡器离线后WiFi Calling状态未同步修复
提交: 6d699c6
文件: cmd/vonet/main.go

**问题描述**:
PCSC读卡器(如Reader-TEST000000030100)硬件断开后，前端仍显示"WiFi-Calling · 全部就绪"。
原因是reconcile循环检测到设备离线时，仅在`state.Enabled==true`时才禁用WiFi Calling。
由于生命周期处理器可能被PCSC长时间操作阻塞(30+秒超时)，`state.Enabled`可能已经
通过其他路径变为false，但数据库中的`VoWiFiEnabled`字段未被更新，导致前端读取到过期状态。

**修复内容**:
- reconcile离线检测逻辑改为：当设备离线时，**始终**更新数据库将`VoWiFiEnabled`设为false
- 数据库更新独立于运行时状态检查，确保即使`state.Enabled`已为false，数据库也会被正确更新
- 仅在`state.Enabled==true`时才调用`RequestEnabled(false)`通知运行时

**修复前逻辑**:
```
if state.Enabled {
    RequestEnabled(false)
    // 数据库未更新
}
```

**修复后逻辑**:
```
if config.VoWiFiEnabled {
    config.VoWiFiEnabled = false
    database.UpsertDevice(config)  // 始终更新数据库
}
if state.Enabled {
    RequestEnabled(false)           // 仅运行时仍启用时才通知
}
```

### 38. 开发者模式设备配额上限扩展 + IMEI 池更新 + 添加设备占位符乱码修复

提交: 60e41c0
文件: internal/developer/settings.go, web/src/components/settings/DeviceQuotaCard.tsx, web/src/pages/SettingsPage.tsx, internal/vowifi/pcsc_adapter.go, web/src/components/devices/DeviceAddDialog.tsx

**变更一：设备配额上限 1-10 扩展为 1-999**

- internal/developer/settings.go：MaxDeviceLimit 常量从 10 改为 999
- web/src/components/settings/DeviceQuotaCard.tsx：前端 Input 的 max 属性 fallback 从 ?? 10 改为 ?? 999
- web/src/pages/SettingsPage.tsx：前端校验 maximum 的 fallback 从 ?? 10 改为 ?? 999
- 后端 API（internal/server/developer_settings.go）和测试（internal/developer/settings_test.go）通过引用 MaxDeviceLimit 常量自动适配

**变更二：PCSC 读卡器 IMEI 池替换**

- internal/vowifi/pcsc_adapter.go：将 pcscIMEIPool 从 6 个旧 IMEI 替换为 8 个新 IMEI：
  - 866069053659148、866069052438940、866069053083133、866069053222707
  - 866069051202057、866069054181407、866069050999893、866069051190162
- 下游 rotateIMEI()、CurrentIMEI() 等通过 len(pcscIMEIPool) 动态读取，自动适配

**变更三：修复添加设备 ID 输入框占位符乱码**

- web/src/components/devices/DeviceAddDialog.tsx:192：t( 渚嬪 ec20_3) 改回 t(例如 ec20_3)
- 乱码原因：commit c78bf26 编辑文件时以 GBK 编码重写，UTF-8 中文 例如（E4BE8B E5A682）被错误解释为 渚嬪 + 私用区字符
- 修复后与 web/src/lib/i18n-en.ts:755 中的 i18n key 匹配，英文翻译 e.g. ec20_3 恢复生效
---

### 39. 读卡器ID字段统一使用USBSerialNumber
提交: 58e1521
文件: internal/pcsc/service.go, internal/server/device_api.go, cmd/vonet/main.go, internal/vowifi/pcsc_adapter.go, internal/vowifi/integration/at.go

**问题描述**:
读卡器设备ID由 `Reader-` + `ReaderName` 组成，其中 `ReaderName` 包含序列号和USB接口号（如 `TEST000000010000` 末尾的 `0000` 是接口号）。
当读卡器被移到不同的USB端口时，接口号会发生变化，导致设备ID不稳定。

**修改内容**:
将读卡器设备ID后缀统一改为仅使用 `USBSerialNumber`（纯序列号，不含接口号），移除了 `ReaderName` 和 `Name` 的备选逻辑。

变更前后对比：

| 读卡器 | 变更前ID | 变更后ID |
|--------|----------|----------|
| 读卡器1 | `Reader-TEST000000010000` | `Reader-TEST00000001` |
| 读卡器2 | `Reader-TEST000000040100` | `Reader-TEST00000004` |
| 读卡器3 | `Reader-TEST000000050200` | `Reader-TEST00000005` |
| 读卡器4 | `Reader-TEST000000060300` | `Reader-TEST00000006` |
| 读卡器5 | `Reader-TEST000000030400` | `Reader-TEST00000003` |

涉及函数：
- `DeviceID()` - 移除 ReaderName/Name 备选，仅使用 USBSerial
- `suggestedDeviceID()` - 移除 ReaderName 备选
- `physicalForConfig()` - 匹配逻辑从 ReaderName 改为 SerialNumber
- `physicalMatchesConfig()` - 移除 ReaderName 精确匹配 + SerialNumber 备选的双重逻辑，改为仅 SerialNumber
- `fillConfigFromPhysical()` - ControlDevice 存储值从 ReaderName 改为 SerialNumber
- `configureDeviceBackends()` - ControlDevice 同步逻辑改为 SerialNumber
- `provisionDiscoveredDevices()` - control 变量赋值改为 SerialNumber
- `matchPCSCDevice()` - 移除 ReaderName 备选匹配
- `PCSCAdapter.ReadIdentity()` - IMEI 映射 key 从 ReaderName 改为 USBSerial
- `physicalMatchScore()` - PCSC 匹配逻辑改为仅 SerialNumber

**影响范围**:
- 已有设备的 `ControlDevice` 字段会在下次启动时通过同步逻辑自动更新为 SerialNumber 格式
- `matchReader()` 底层函数通过 `USBSerial` 备选路径确保已有设备仍能正确匹配物理读卡器
- 编译通过，pcsc/vowifi 测试全部通过

---

### 40. 修复读卡器设备添加冲突导致名称被覆盖的Bug
提交: 83590b3
文件: internal/server/device_api.go

**问题描述**:
添加新读卡器设备时，前端提示"添加成功"(HTTP 201)，但实际未创建新设备，
而是将设备列表中最后一个设备的名称修改为用户为新设备输入的名称。

**根因分析**:
POST handler 存在三个缺陷：

1. **冲突检查时序问题**：首次冲突检查(约line 241)使用前端传入的 payload.ID，
   但后续自动生成 Reader- + USBSerialNumber 覆盖了 payload.ID，
   生成后的 ID 从未被重新检查是否已存在。

2. **continue 而非拒绝**：existing.ID == payload.ID 时仅 continue 跳过循环，
   不返回错误，导致后续 UpsertDevice 的 ON CONFLICT DO UPDATE SET name = ...
   静默覆盖已有设备的名称。

3. **字段不一致风险**：DeviceID() 使用 reader.USBSerial，
   POST handler 使用 selected.Candidate.SerialNumber，两者可能不同。

**修复内容**:
- 修复1：自动生成 ID 后重新检查冲突，若已存在则返回 409 Conflict
- 修复2：existing.ID == payload.ID 时返回 409 Conflict 错误，而非 continue
---

### 41. 修复删除读卡器设备后仍返回409冲突的Bug
提交: 83590b3
文件: internal/vowifi/runtime/manager.go

**问题描述**:
删除PCSC读卡器设备后，立即重新添加时返回409 "a device with this ID already exists"，
但设备列表中并看不到该设备。等待几分钟后才能成功添加。

**根因分析**:
VoWiFi Manager的 Remove() 函数在关闭 orchestrator 后立即返回，
但 watch goroutine 可能仍在执行 onState() 回调（将 VoWiFi 状态写入数据库）。
此时 DELETE handler 调用 DeleteDevice()，与 onState 的数据库写入产生竞争：
- SQLite 写锁被 onState 持有，DeleteDevice 的事务可能超时失败
- 或者 onState 在设备删除后尝试 INSERT vowifi_runtime，触发 FK 约束错误
- 前端做了乐观删除（立即从UI移除），但后端删除实际失败，设备仍在数据库中

**修复内容**:
在 entry 结构体中添加 watchDone channel，在 watch goroutine 退出时关闭。
Remove() 在返回前等待 watchDone 关闭，确保 watch goroutine 完全退出后
才将控制权交还给 DELETE handler，从而避免与 DeleteDevice 的数据库写入竞争。

变更点：
- entry 结构体：添加 watchDone chan struct{} 字段
- Ensure() / Register()：创建 entry 时初始化 watchDone channel
- watch()：添加 defer close(done)，goroutine 退出时关闭 channel
- Remove()：在 orchestrator.Close() 之后等待 watchDone channel 关闭
---

### 42. DITO 读卡器 WiFi Calling 身份改写与 ISIM 身份读取修复
提交: bf76ee9
**问题描述**:

读卡器类型连接 DITO 网络时，WiFi Calling 持续失败：

```text
ike: IKE_AUTH EAP round 1: ike: responder reported fatal notification 10500
```

大疆设备可以正常完成 WiFi Calling，读卡器设备一直失败。日志显示读卡器 USIM 返回的漫游 IMSI 前缀为 `204-043-610`，但原 DITO Profile 只改写 `204-047-616`，因此 EAP-AKA 使用了错误的订阅身份，ePDG 在 AKA 处理前直接拒绝身份。

**修改内容**:

- `internal/vowifi/carrier_compat.go`
  - 支持 `subscriber_imsi_rewrites` 多条 IMSI 前缀改写规则。
  - 改写时按最长前缀匹配，兼容原有单条 `subscriber_imsi_rewrite` 配置。
  - 新增 `EffectiveSubscriberIdentity()`，优先使用 ISIM 配置身份，其次使用 Carrier Profile 改写，最后回退 USIM 原始 IMSI。
- `internal/vowifi/carrier_profiles.json`
  - DITO Profile 新增：
    - `204047616 -> 515661015`
    - `204043610 -> 515661037`
  - 增加 `204043610`、`204043` 匹配条件。
- `internal/pcsc/service.go`
  - 增加 ISIM AID 发现和可选读取逻辑。
  - 读取 `EF_IMPI`、`EF_IMPU` 中的配置 IMSI，并解析标准 NAI 格式。
  - ISIM 不存在、读取失败或格式不完整时静默回退，不影响原有 USIM 身份读取。
- `internal/pcsc/types.go`、`internal/vowifi/types.go`
  - 增加 `ProvisionedIMSI` 字段并传递配置身份。
- `internal/vowifi/pcsc_adapter.go`
  - 将 ISIM 配置身份传入 VoWiFi 身份链路。
  - DITO 漫游 IMSI 的 Home PLMN 统一归一化为 DITO 路由 PLMN。
- `internal/vowifi/ike/eap.go`
  - EAP-AKA permanent identity 使用有效订阅身份。
- `internal/vowifi/ims/provider.go`
  - IMS private/public identity 使用相同的有效订阅身份。
- `internal/vowifi/ike/provider.go`
  - 新增去敏身份诊断日志，输出 `identity_source`、`rewrite_rule_present`、改写前后 IMSI 前缀、NAI realm 等信息，不输出完整 IMSI/ICCID/NAI。
- `internal/device/carrier_db.go`
  - 将 `204043610` 和 `515661037` 识别为 DITO 身份。

**验证结果**:

- `go test ./internal/pcsc ./internal/vowifi ./internal/vowifi/ike ./internal/vowifi/ims`：通过。
- `go test ./internal/device -run 'DITO|CarrierForSIM'`：通过。
- `go vet ./internal/pcsc ./internal/vowifi ./internal/vowifi/ike ./internal/vowifi/ims`：通过。
- `go build ./...`：通过。
- 读卡器类型已完成实际 WiFi Calling 验证。

**风险说明**:

- `204043610 -> 515661037` 是根据当前日志和大疆成功身份推导的兜底改写；当 ISIM 可读取时，优先使用 ISIM 中的真实配置 IMSI。
- ISIM 不支持或读取失败时，系统继续使用 DITO Profile 改写，不阻断原有设备流程。
- 全量测试中仍存在与本次修改无关的既有失败：`internal/server` 测试替身缺少 `SendDTMF`、开发者配额测试、设备发现测试以及 Windows 下 `qmiport` 文件占用。

---

### 43. 测试适配与日志历史顺序稳定性修复
提交: 33247a8

**修改范围**:

- 仅修改测试代码，不修改任何生产逻辑。
- 修复仓库原有测试失败，并消除 `loghub` 历史顺序测试的偶发不稳定问题。

**修改内容**:

- `internal/developer/settings_test.go`
  - 配额夹具改为使用 `MaxDeviceLimit + 1` 和 `MaxSMSHourlyLimit + 1`，不再依赖硬编码的 `99` 是否超过限制。
- `internal/device/manager_test.go`
  - 为测试读卡器补充 `USBSerial`，并验证自动 ID 生成结果为 `Reader-USBSerial`。
- `internal/qmiport/lease_test.go`
  - 使用测试专用 `portHandle` 返回稳定的文件标识，避免 Windows 下真实文件句柄占用和替换节点识别不稳定。
- `internal/server/call_api_test.go`、`internal/server/telegram_bot_test.go`
  - 测试替身补充 `SendDTMF` 接口实现，使其继续满足当前生产接口。
- `internal/server/call_notifications_test.go`
  - 拆分并修正空号码活动会话测试，验证 `parseCLCC` 过滤幻影呼叫的行为。
- `internal/server/region_test.go`
  - 测试预期适配中国区域不再阻断 VoWiFi 的当前逻辑，并统一区域测试服务器辅助函数。
- `internal/loghub/hub_test.go`
  - 测试日志改用显式递增时间戳，分别设置为基准时间、`+1ns`、`+2ns`，避免 Windows 上连续日志时间戳相同导致排序断言偶发失败。

**验证结果**:

- `go test ./... -count=1`：全部通过。
- `go test ./... -count=1 -shuffle=on`：全部通过。
- `go test ./internal/loghub -run '^TestHubHistoryFiltersAndBounds$' -count=100`：全部通过。
- `git diff --check`：通过。

---

### 44. WAP Push MMS 显示解码补强
提交: d1b30d0

本次同步上游 12ce8f0 后，补充覆盖两个显示链路缺口：

- `internal/server/sms_api.go`：modem SMS 持久化时保存 `RawUserData` 到 `Extra.raw_user_data`，单段 WAP Push 被解码为 Subject 后仍保留原始 WSP/MMS 载荷。
- `internal/server/telegram_bot.go`：Telegram 新增短信通知统一通过 `smsdecode.Preview` 预览旧十六进制 WAP Push，避免 Telegram 单独显示原始 WSP dump。
- `internal/server/sms_api_test.go`：验证 modem 同步保留 `raw_user_data`。
- `internal/server/telegram_bot_test.go`：验证 Telegram 文本显示 WAP Push Subject 而不是十六进制载荷。

**验证结果**：`go test ./... -count=1`、`go build ./...` 均通过。

---

### 45. 读卡器固定/随机 IMEI 租约与 IMEI 池管理
提交: 2ac5686

**功能范围**:

- 为 USB SIM 读卡器建立独立的 IMEI 池和设备租约模型，支持固定 IMEI 与随机 IMEI 两种模式。
- 添加设备时可在“IMEI 绑定”下拉框中选择“随机 IMEI”或 IMEI 池中的空闲 IMEI。
- 设备配置编辑页使用相同方案；固定 IMEI 保存后保持不变，切换回随机模式后由后续 VoWiFi 尝试进行轮换。
- 侧边栏新增“IMEI 池”页面，支持添加、修改、删除 IMEI，并显示租用状态和绑定设备。

**后端修改**:

- `internal/store/migrations.go`：新增 `pcsc_imei_pool`、`pcsc_imei_leases` 独立表；租约通过唯一约束保证同一 IMEI 只能绑定一个读卡器，并在设备删除时级联释放。
- `internal/store/pcsc_imei.go`：实现 IMEI 池 CRUD、固定/随机租约分配、模式切换、随机轮换和池耗尽处理。
- 读卡器创建和编辑分别通过 `CreateDeviceWithPCSCIMEILease()`、`UpdateDeviceWithPCSCIMEILease()` 在同一事务中保存设备与租约，任一步骤失败均回滚。
- 固定 IMEI 禁止重复绑定；固定和随机分配均避开已租用 IMEI 以及普通蜂窝设备已保存的 `modem_imei`，避免短信通知错误归属。
- 池耗尽时新增读卡器返回 HTTP 409 `pcsc_imei_pool_exhausted`；随机轮换无可用值时跳过本次轮换并保留当前 IMEI。
- 读卡器配置强制清空旧 `modem_imei`，IMSI/IMEI 归属改由独立租约和 VoWiFi 身份链路提供。
- `internal/server/device_api.go`：添加和编辑接口返回 `pcsc_imei`、`pcsc_imei_mode`；租约变化且 VoWiFi 已启用或活动时请求重连。
- `internal/server/pcsc_imei_api.go`：新增 IMEI 池查询和管理 API，非法 IMEI 返回 HTTP 400，占用、重复或池耗尽返回 HTTP 409。
- `internal/vowifi/pcsc_lease_adapter.go`：VoWiFi 读卡器身份改用数据库租约，固定模式不轮换，随机模式继续按 VoWiFi 尝试/重试轮换。

**设备类型与物理匹配**:

- 读卡器类型双向不可变：读卡器不能改成其他类型，非读卡器也不能改成读卡器类型；统一返回 HTTP 409 `immutable_reader_device_type`。
- 添加设备时同时验证物理硬件类型，PCSC 硬件只能配置为读卡器，非 PCSC 硬件不能配置为读卡器。
- `findDiscoveredDevice()`、`physicalMatchesConfig()`、启动阶段匹配和 VoWiFi 映射统一跳过 IMEI 匹配，使用 USB 序列号、USB 路径或发现键定位 PCSC 设备。

**前端修改**:

- `web/src/components/devices/PCSCIMEISelect.tsx`：新增共享 IMEI 下拉框；第一项为“随机 IMEI”，其余为 IMEI 池值，已被其他读卡器绑定的值禁用。
- `web/src/components/devices/DeviceAddDialog.tsx`、`web/src/components/devices/DeviceConfigTab.tsx`：读卡器添加和编辑页接入固定/随机 IMEI 选择，并锁定读卡器设备类型。
- `web/src/pages/IMEIPoolPage.tsx`：新增 IMEI 池管理页，支持添加、修改、删除和租用状态展示。
- `web/src/components/shell/AuthenticatedShell.tsx`、`web/src/App.tsx`：侧边栏在仪表盘和设备管理之间新增“IMEI 池”入口。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：8 项测试全部通过。
- `npm run build`：通过。
- `git diff --check`：通过。

---


### 46. 代理管理路由重构与自动设备 IMEI 租约修复
提交: 80d55e4

**一、代理管理路由重构**

**功能范围**:

- 代理绑定主体由单一 ICCID 改为 `device_id + ICCID`，同一张卡可以绑定多个有序代理。
- WiFi Calling 按代理顺序尝试；每个代理失败 3 次后切换到下一个代理，最后一位失败后回到第一位循环，直到某个代理成功。
- `IMSReady == true` 作为代理成功判定点；成功代理成为粘性路由，连接中断后优先用该代理重试 3 次，仍失败才切换到下一位代理。
- 手动绑定优先于 MCC 国家规则；卡片存在手动代理绑定时只使用手动顺序，没有手动绑定且命中 MCC 规则时才使用 MCC 绑定的代理。

**数据与后端**:

- `internal/store/proxy_routes.go`、`internal/store/proxy_routes_query.go`：新增 `device_proxy_binding_groups`、`device_proxy_binding_routes`、`device_proxy_route_state` 独立表，分别保存设备/ICCID 绑定组、有序代理路由和运行状态。
- 数据库初始化兼容旧 `device_proxy_bindings`，将已有单代理绑定转换为新绑定组和优先级 0 路由；新 schema 使用唯一约束和外键级联保证数据一致性。
- `internal/store/proxy_route_state_vowifi.go`：持久化当前路由、成功粘性路由、失败次数和最近成功时间，服务重启后继续未完成的故障切换。
- `internal/vowifi/proxy_route_types.go`、`internal/vowifi/proxy_selector.go`：实现多代理路由计划、3 次失败轮换、循环切换、成功粘性和断联重试状态机。
- `internal/vowifi/integration/proxy_routes.go`：手动绑定路由优先；仅在无手动绑定时动态解析 MCC 国家规则。
- `internal/vowifi/orchestrator.go`、`internal/vowifi/types.go`、`internal/vowifi/integration/store.go`：接入路由状态读写、代理成功判定和运行状态快照。
- `internal/server/proxy_binding_groups_api.go`、`internal/server/proxy_binding_validation.go`、`internal/server/proxy_api.go`：新增绑定组及路由顺序查询、创建、替换、删除 API，校验设备、ICCID、重复代理、空路由和禁用代理。
- 删除设备时通过外键级联清理该设备的绑定组、代理路由和路由状态；删除上游代理时清理相关路由和 MCC 规则。
- 已有稳定 IMS 会话时修改路由不强制打断当前连接，新的代理顺序在后续重连时生效。

**前端修改**:

- `web/src/pages/ProxyPage.tsx`、`web/src/components/proxy/UpstreamSection.tsx`：代理管理页拆分为“上游代理管理”和“SIM / Profile 绑定”两个独立区块；上游区块专注新增、编辑、启停、连通性检测和删除。
- `web/src/components/proxy/ProxyBindingSection.tsx`、`web/src/proxy_types.ts`：绑定时先选择一个在线设备，再选择该设备下的一张卡，避免一次性读取所有设备的卡。
- 同一设备/ICCID 可添加多个代理并调整顺序；离线设备的绑定以只读方式展示，避免修改后无法立即生效。
- MCC 国家规则保持单代理绑定，并在页面中明确手动绑定优先。

**二、自动设备 IMEI 租约与配置完整性修复**

**问题现象**:

- 二进制首次启动自动创建的 USB SIM 读卡器虽然进入设备列表，但没有 IMEI 绑定。
- 打开设备配置页时所有字段为空；VoWiFi 持续报错 `read PCSC IMEI lease: store: not found` 并自动重试。
- 手动添加的读卡器正常，原因是手动路径在同一事务中创建设备和 `pcsc_imei_leases`，自动路径只调用了普通 `UpsertDevice()`。

**自动设备后端修复**:

- `cmd/vonet/main.go`：自动预配置的读卡器改用 `CreateDeviceWithPCSCIMEILease()`，默认申请随机 IMEI，并在设备与租约同一事务中创建。
- IMEI 池耗尽时跳过当前读卡器但继续处理后续设备，不再留下无租约的读卡器记录。
- 启动阶段增加已有读卡器租约自愈；缺失租约会被随机分配，已有固定或随机租约保持不变。
- `internal/store/devices.go`：普通 `UpsertDevice()` 和 `SaveDeviceState()` 对读卡器统一确保租约存在，封住绕过事务化创建接口产生坏数据的路径。
- `internal/store/pcsc_imei.go`：新增 `EnsurePCSCIMEILease()` 修复接口，并限制显式创建/更新接口只能处理读卡器设备。
- `internal/store/migrations.go`：新增迁移 28，回填已有读卡器缺失的随机租约，避免重复占用或普通 Modem IMEI 冲突；同时清空读卡器遗留的 `modem_imei` 并回填 USB 序列号。

**硬件身份修复**:

- `internal/server/device_api.go`：统一保存 `usb_serial_number`、`usb_vendor_id`、`usb_product_id`，自动与手动创建使用相同的身份字段。
- 启动时为已有自动设备补齐缺失的 USB 硬件身份，提高 PCSC 读卡器换 USB 口或重新枚举后的匹配稳定性。
- `internal/server/pcsc_imei_api.go`：修正租约缺失时的错误描述，避免被误认为只是 IMEI 池条目不存在。

**测试覆盖**:

- 代理管理：`internal/store/proxy_routes_test.go`、`internal/vowifi/proxy_selector_test.go`、`internal/vowifi/integration/proxy_routes_test.go` 覆盖多代理顺序、循环故障切换、粘性重试、手动绑定优先和持久化状态恢复。
- 自动创建读卡器后设备、随机租约和 USB 硬件身份必须同时存在。
- IMEI 池耗尽时跳过读卡器并继续创建后续非读卡器设备。
- 已有读卡器缺失租约时，启动流程能够自动修复。
- 普通 `UpsertDevice()` 自动创建租约且更新设备时保留现有租约。
- 迁移 28 能为多个缺失租约的读卡器分配唯一 IMEI，并排除固定租约和普通 Modem 已使用的 IMEI。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：8 项测试全部通过。
- `npm run build`：通过。
- `git diff --check`：通过。

---

### 47. IMEI 预设池扩充与列表分页
提交: da2d6b6

**IMEI 预设池扩充**:

- 在原有 8 个预设 IMEI 基础上新增 12 个，`internal/pcscimei/pool.go` 的默认池扩展为 20 个值。
- 新增 IMEI：`866069053452247`、`866069053461198`、`863285069344664`、`863212061358591`、`866069051772448`、`866069053101505`、`866069052517214`、`863212061734262`、`863212060607550`、`866069052477120`、`863212060571335`、`863285068498933`。
- `internal/vowifi/pcsc_adapter.go`：VoWiFi 兼容随机池改为引用统一的 `pcscimei.DefaultPool`，避免两份预设列表继续产生偏差。
- `internal/store/migrations.go`、`internal/store/store.go`：新增迁移 29 并将 schema 版本升级到 29；已有数据库启动时通过 `INSERT OR IGNORE` 自动补齐新增 IMEI，新建数据库也会得到完整 20 个预设值。
- `internal/pcscimei/pool_test.go`、`internal/store/pcsc_imei_test.go`：验证默认池均为合法且唯一的 15 位 IMEI，并覆盖旧数据库迁移后的完整池内容。

**列表分页**:

- `web/src/pages/IMEIPoolPage.tsx`：IMEI 池列表增加分页，每页固定显示 10 条，并显示总条数、页码和上下页按钮。
- `web/src/components/proxy/UpstreamSection.tsx`：代理管理页的上游代理列表增加相同样式和规则的分页，每页固定显示 10 条。
- 两处列表只渲染当前页数据；删除、刷新或轮询导致总页数减少时，自动收敛到有效页码，不保留越界页。
- 分页复用现有 `Pagination` UI 组件，不改变 IMEI 池和上游代理 API，也不提供每页条数切换。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：8 项测试全部通过。
- `npm run build`：通过。
- `git diff --check`：通过。

---

### 48. 代理绑定变更即时重连、路由重置与空绑定 MCC 回退
提交: 0d6f164

**问题修复**:

- 修复绑定代理创建、更新、删除或调整顺序后 Wi-Fi Calling 不立即重新建立的问题。原逻辑在 `IMSReady` 为 true 时跳过重连，导致继续使用旧代理；现在所有绑定路由变更都会请求 VoWiFi 重连，并保留运行时重连合并机制，避免并发操作。
- 修复代理顺序变化后仍从旧位置继续尝试的问题。`internal/vowifi/proxy_selector.go` 对路由计划签名进行比较，绑定列表、代理顺序或路由计划变化时清空旧的 `routeIndex`、粘性路由、失败计数和成功状态，从第一个代理重新开始；切换 ICCID 时仍保留已有路由状态恢复能力。
- 修复删除绑定代理到 0 个后无法保存的问题。前端不再禁用空路由的“保存顺序”，后端允许 `proxy_ids` 为空并删除手动绑定组；路由解析随后自动回退到 MCC 国家规则或直连模式。

**绑定管理与 Profile 缓存优化**:

- `web/src/components/proxy/ProxyBindingSection.tsx`、`web/src/components/proxy/bindingModel.ts`、`web/src/components/proxy/binding.css`：重新设计 SIM / Profile 绑定区域，展示设备、Profile、ICCID、绑定代理和绑定状态，支持筛选、搜索、拖拽或按钮调整代理顺序，并将新增绑定弹窗和代理编辑抽屉扩大到代理管理页面级别。
- eSIM Profile 清单改为打开“新增绑定”时读取，并缓存读取结果；后台只刷新绑定关系，不再每 10 秒扫描 APDU，避免选择卡片后被扫描结果刷新而丢失选择。
- `web/src/components/ui/Drawer.tsx`、`web/src/components/ui/Modal.tsx`、`web/src/pages/ProxyPage.tsx`、`web/src/lib/i18n-en.ts`：调整弹窗/抽屉尺寸、页面布局和提示文案，保持上游代理与 MCC 国家规则区域原有功能不变。

**测试覆盖**:

- `internal/server/proxy_binding_test.go`：覆盖稳定 IMS 状态下绑定变更仍会重连、空路由保存删除绑定组并触发 MCC 回退。
- `internal/vowifi/proxy_selector_test.go`：覆盖代理顺序变化后从第一个代理重新开始，并保留多代理故障切换与粘性重试行为。
- `web/test/proxyBindingModel.test.mjs`：覆盖 Profile 缓存、全局绑定卡片合并、读取请求防抖和刷新后的选择状态。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：14 项测试全部通过。
- `npm run build`：通过，仅有原有大 chunk 警告。
- `git diff --check`：通过。


---

### 49. PCSC 来电轮询不再走 AT，并增强 reader not found 诊断
提交: 6d014e5
**问题现象**:

- 部署同步更新后的二进制后，`Reader-TEST00000001` 等 PCSC 读卡器持续出现：

  `backend=pcsc hardware_kind=pcsc error=device has no usable AT port`

- 设备发现和 PCSC 状态刷新正常，`registration_source=pcsc`，但 VoWiFi 关闭时蜂窝来电轮询仍尝试执行 `AT+CLCC`。
- 该问题影响所有 PCSC 读卡器，并非单个设备的 IMEI、租约或 PCSC 发现配置错误。

**根因**:

- 本次上游 `048b929` 为了解决普通蜂窝 Modem 在数据关闭时漏检来电，删除了 `pollCellularCalls()` 中的 `NetworkEnabled` 跳过条件。
- `callTransport()` 在 VoWiFi 尚未 `IMSReady` 时返回 `cellular`，于是 PCSC 读卡器被当作蜂窝 AT Modem。
- `ExecuteAT()` 最终调用 `clientLocked()`；PCSC 没有 AT 口，因此返回 `ErrNoATPort`。
- 3 秒来电轮询反复执行，PCSC 快照每 30 秒刷新成功后会清除旧错误，导致日志每隔约 30 秒重新出现。

**修复内容**:

- `internal/server/call_notifications.go`：`pollCellularCalls()` 先解析物理设备，再按 `usb_sim_reader` 或 `HardwareKind == pcsc` 跳过来电轮询；普通蜂窝 Modem 在 `NetworkEnabled=false` 时仍继续执行 `AT+CLCC`，保留上游修复意图。
- `internal/pcsc/service.go`：PCSC reader 打开失败时增加 `usb_path`、`reader_name`、`usb_serial` selector 诊断；保留 `errors.Is(err, pcsc.ErrReaderNotFound)`，便于区分 PCSC 重扫/断连和 selector 匹配问题。
- `internal/server/call_notifications_pcsc_test.go`：覆盖 PCSC 在 `NetworkEnabled=false/true` 时均不调用 AT 命令，并验证普通 Modem 数据关闭时仍调用一次 `AT+CLCC`。
- `internal/pcsc/reader_error_test.go`：覆盖 reader not found 错误包含完整 selector 信息。

**验证结果**:

- `go test ./internal/pcsc ./internal/server` 通过。
- `go test ./...` 全部通过。
- `git diff --check` 通过。
- 本次修复不改变 PCSC VoWiFi 的 AKA、IMEI 租约、DITO/ProvisionedIMSI 或代理路由逻辑。

---

### 50. E911 上游同步后的身份、IMEI 与并发加固
提交: `conflict-analysis` 分支同步更新

**同步背景**:

- 合入 VoNet-reference `0739ac2`、`a3bfd01`、`2d023a7` 的 E911、entitlement 和 IKE COOKIE 更新。
- 保留本地 PCSC IMEI、DITO/Provisioned IMSI、代理路由、设备匹配和小区锁定修改。
- 保留 `docs/E911.zh-CN.md` 中的实卡验证限制、协议依据和 API 说明。

**修复内容**:

- `internal/vowifi/e911/carrier.go`：运营商网络识别同时检查 EF_AD MNC、标准 MNC 补零形式和 IMSI 前六位，避免 MNC 长度为 2 位时误判 T-Mobile、RedPocket/AT&T 等线路。
- `internal/vowifi/e911/entitlement.go`、`ts43.go`：TS.43 后续 token 查询使用 `EffectiveSubscriberIdentity()`，与 EAP-AKA 的 Provisioned IMSI 和 Carrier Profile 改写保持一致。
- `internal/vowifi/types.go`、`orchestrator.go`、`pcsc_adapter.go`、`pcsc_lease_adapter.go`：新增不轮换 IMEI 的 provisioning 身份读取接口；E911 会话不消费 PCSC 随机 IMEI 轮换，普通 VoWiFi 尝试/重试继续按原策略轮换。
- `internal/vowifi/runtime/manager.go`：`WithSIM` 回调使用可取消 context，并跟踪 provisioning 状态；并发生命周期请求被阻塞，`Reset/Remove/Close` 会取消正在进行的 E911/entitlement 请求。
- `web/src/components/devices/E911SetupDialog.tsx`、`DevicesPage.tsx`：修正未实卡验证提示、E911 loading 状态和成功后关闭行为；移除旧 Websheet 组件、样式和错误文案。
- 旧 `e911_address:*` 数据继续保留但不作为运营商确认凭据；新流程只写入 `e911_confirmed:*`。

**测试覆盖**:

- `internal/vowifi/e911`：覆盖两位 MNC 识别和 TS.43 有效 IMSI 查询。
- `internal/vowifi/pcsc_lease_adapter_test.go`：覆盖 provisioning 读取不轮换 IMEI、普通读取继续轮换。
- `internal/vowifi/orchestrator_test.go`：覆盖 `WithSIM` 优先使用稳定身份读取接口。
- `internal/vowifi/runtime/manager_test.go`：覆盖 provisioning 阻塞生命周期操作，且 `Remove` 能取消活动会话。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：25 项全部通过。
- `npm run build`：通过，仅有原有大 chunk 警告。
- `git diff --check`：通过。
- `go vet`：仅报告既有 `cell_lock_api_test.go` 未命名结构体字面量告警。

---

### 51. USSD/USSI 路由、会话生命周期与 Telegram 轮询加固
提交: `conflict-analysis` 分支同步更新

**同步背景**:

- 合入 VoNet-reference `027379d`、`0333083` 的 USSD/USSI 统一路由和 Telegram Admin ID 轮询修复。
- 保留 WAP Push、PCSC IMEI、设备匹配、DITO/Provisioned IMSI、代理路由和 E911 修改。

**修复内容**:

- 网页和 Telegram 统一调用 `startUSSD`、`continueUSSD`、`cancelUSSD`；IMS 使用配置设备 ID，蜂窝使用物理设备 ID。
- VoWiFi 启用后不再在 USSI 不可用时回退 AT+CUSD，避免提交到错误通道或掩盖 IMS 状态问题。
- USSI token 精确绑定 SIP dialog，取消失败保留会话供重试；BYE 返回 481 时视为远端会话已关闭。
- `internal/server/ussd_sessions.go`：服务端 USSI token 增加 15 分钟 TTL 和惰性清理。
- `internal/device/types.go`、`internal/server/ussd.go`：USSD 响应增加 `local/cellular/ims` transport channel。
- `internal/vowifi/ims/ussi_runtime.go`：同一 dialog 的继续输入和取消通过 per-dialog 锁串行化，避免 BYE 与 INFO 交错。
- 统一 USSI/session 错误码；Telegram 未配置 `admin_id` 时不再调用 `getUpdates`。

**测试覆盖**:

- USSD 网页/Telegram 共用路由、配置 ID/物理 ID、禁止 VoWiFi 回退和取消重试。
- USSI token 过期清理、transport channel、并发 continue/cancel 和 BYE 状态保留。
- Telegram Admin ID 空值跳过轮询，并保留 WAP Push 与 SendDTMF 回归测试。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：29 项全部通过。
- `npm run build`：通过，仅有原有大 chunk 警告。
- `git diff --check`：通过。

---

### 52. 英国 O2 Wi-Fi Calling 注册、拨号与来电接听修复
提交: 本次 O2 Wi-Fi Calling 本地修复

**问题背景**:

- 英国 O2 SIM 在 VoNet 中可以启动部分 VoWiFi 阶段，但 Wi-Fi Calling 注册、拨打电话和接听来电先后出现失败。
- 处理期间使用安卓手机实机抓取 IKE/IPsec、RTP 和 SIP 流程，并与 VoNet 的 O2 Carrier Profile、运行日志和实际 SIP 交互逐项对比。
- 排查只使用脱敏日志和合成测试数据，真实 IMSI、ICCID、IMEI、读卡器序列号、号码、内部 IP 及抓包数据不写入仓库。

**一、注册阶段问题**:

- O2 REGISTER 最初会被网络以位置相关错误拒绝，VoNet 日志中表现为注册请求发出后收到 `403 Service not allowed in this location`。
- 对比安卓 O2 注册消息后发现 VoNet 的 O2 Profile 仍发送占位 WLAN `P-Access-Network-Info`，其位置参数并不对应 O2 实际接受的注册流程。
- O2 Profile 同时需要更长的注册有效期、`path,sec-agree`、完整 IMS 能力声明，以及 PC/SC 路径缺少 GID1 时的 ICCID 前缀匹配。
- 修复 `internal/vowifi/carrier_profiles.json` 和 `internal/vowifi/carrier_compat.go`：
  - O2 Profile 在 GID1 不可用时使用运营商 ICCID 前缀匹配。
  - 禁用不准确的 WLAN PANI，避免 O2 按位置策略拒绝 REGISTER。
  - 注册有效期调整为 O2/安卓流程允许的 600000 秒，并放宽 Carrier Profile 校验上限。
  - REGISTER 补齐 O2 所需的 `Supported`、`Allow`、Contact feature tags 和 `Accept-Contact`。
- 修复后 O2 注册进入 `registered`、`ims_ready=true`、`tunnel_ready=true` 状态。

**二、拨打电话问题**:

- O2 注册成功后，外呼初期出现接通后短时间内自动挂断和双方无声。
- 日志与 RTP 抓包显示 SIP 外呼事务可以完成，但媒体协商和 RTP 路由仍存在问题。
- 主要修复：
  - RTP 媒体绑定 VoWiFi 隧道内的本地媒体地址，修复 RTP/RTCP 路由缺失。
  - 对 O2 Offer 的 AMR/AMR-WB 动态 payload 和 FMTP 进行精确协商，不再根据 `m=` 行中第一个动态 payload 猜测。
  - 保留 Offer 的 RTP profile、`rtcp-mux`、`rtcp-rsize` 和 AMR mode-set/fmtp 参数。
  - 补齐外呼 2xx ACK、早期媒体和无 SDP 200 OK 的生命周期处理，避免通话状态提前结束。
  - 浏览器音频在外呼和来电接通后都启动，修复外呼双方无声。
- 修复后外呼可以正常建立、保持通话和正常挂断。

**三、来电接听失败现象**:

- 来电可以进入 VoNet，网页能够显示来电，点击接听后 API 返回成功，但通话仍显示未接通。
- SIP 日志稳定呈现以下流程：
  1. O2 发送 INVITE，VoNet 创建 incoming call。
  2. VoNet 发送可靠 `180 Ringing`，包含 `Require: 100rel` 和 `RSeq`。
  3. O2 发送 PRACK，VoNet 返回 200 OK。
  4. 用户点击接听后，VoNet 返回带 SDP 的 `200 OK`。
  5. VoNet 按 T1/T2 重传 `200 OK`。
  6. O2 始终没有发送 ACK，约十几秒后发送 CANCEL。
  7. CANCEL 原因为 `SIP;cause=408;text="CC_TERM_NOREPLY_TMR_EXPIRED"`。
- 因此该问题不是网页按钮、接听 API 或本地呼叫状态机失败，而是 O2 没有接受/完成 INVITE 的 2xx 对话建立。

**四、逐轮排除过程**:

1. `100rel/PRACK`：
   - 验证 `RSeq`、`RAck` 和 PRACK 200 OK，确认可靠临时响应正常。
   - 该项不是根因。
2. `200 OK` 丢失：
   - 增加 2xx 重传，按 500ms、1s、2s、4s 重发。
   - O2 仍不 ACK，说明单纯重传不能解决响应不合规问题。
3. Contact 与传输：
   - 接听响应改用 REGISTER 已确认的 Contact，并去除 `expires` 参数。
   - 记录 INVITE 来源、主 TCP 连接和受保护 TCP 监听地址。
   - 发现 O2 的 INVITE/PRACK/CANCEL 可能来自与注册连接不同的 P-CSCF 地址；旧的受保护 TCP 来源校验会拒绝这类合法 ACK/请求。
   - 放宽受保护 TCP 来源地址校验，只保留“安全关联已建立”和 TCP 类型检查。
4. SDP payload 猜测：
   - 初始日志只显示 `formats` 列表，无法确认每个 payload 的实际名称。
   - 增加逐候选日志后确认 O2 Offer 中 `96` 是 AMR-WB，`97/98` 是 AMR-NB，不能按 payload 数值大小直接选择。
   - 最终按 codec 名称、时钟、FMTP、octet-align 和 O2 payload family 选择，AMR-WB/AMR-NB 均有回归测试。
5. RTP profile 与 RTCP：
   - Answer SDP 回显 Offer 的 `RTP/AVP`/`RTP/AVPF`、`rtcp-mux` 和 `rtcp-rsize`。
   - 修正 AMR-WB/AMR-NB 编解码 framing、mode-set、payload type 和 telephone-event 时钟。
6. 对话路由：
   - 发现接听 180/200 响应没有回传 INVITE 的 `Record-Route`，O2 无法按对话路由构造 2xx ACK。
   - 所有 INVITE 响应按请求顺序回传 `Record-Route`，并增加多 Record-Route 顺序测试。
7. 会话定时器：
   - O2 INVITE 携带 `Session-Expires: 1800`，原 200 OK 只回显数值，缺少 RFC 4028 要求的 `refresher` 参数。
   - 来电响应改为 `Session-Expires: 1800;refresher=uas`；VoNet 后续通过 UPDATE 作为会话刷新方。
8. DTMF FMTP：
   - O2 Offer 的 telephone-event 参数为 `0-15`，原 Answer 固定写成 `0-16`。
   - Answer 现在保留 Offer 的 DTMF FMTP，避免 O2 将 SDP 判定为不一致。

**最终修复结论**:

- 来电接听失败由多个 SIP/SDP 兼容性问题叠加造成，其中最直接的对话建立问题是：
  - 受保护 TCP 对替代 P-CSCF 来源的过滤过严；
  - INVITE 响应未回传 `Record-Route`；
  - `Session-Expires` 缺少 `refresher=uas`；
  - DTMF FMTP 与 Offer 不一致。
- AMR-WB `payload=96` 经完整 SDP 映射确认是正确选择，不应强制改成 `98`；此前根据 `formats` 列表推断 payload 的方法不准确。
- 修复后接听流程可完成 2xx ACK、进入 active 状态并建立媒体连接。

**代码修改**:

- `internal/vowifi/carrier_profiles.json`、`internal/vowifi/carrier_compat.go`：O2 注册 Profile、ICCID fallback、PANI 和注册有效期。
- `internal/vowifi/ims/call_runtime.go`：PRACK、200 OK 重传、ACK、Contact、Record-Route、会话刷新和接听生命周期。
- `internal/vowifi/ims/rtp_media.go`：AMR/AMR-WB 编解码、O2 payload 映射、FMTP、RTP profile、RTCP 和 DTMF。
- `internal/vowifi/ims/sms_runtime.go`：受保护 TCP 来源校验和 SIP 入站诊断。
- `internal/server/call_api.go`、`web/src/components/call/CallPanel.tsx`：通话历史和外呼/接听音频生命周期。

**测试覆盖**:

- O2 Profile 使用合成 IMSI/ICCID 验证 ICCID fallback、禁用 PANI 和注册头。
- 来电覆盖可靠 180/PRACK、接听 200 OK、ACK、200 OK 重传和 CANCEL 生命周期。
- 多 `Record-Route` 必须完整保留且顺序不变。
- `Session-Expires` 必须返回 `refresher=uas`。
- O2 Offer 的 AMR-WB、AMR-NB、telephone-event 和 FMTP 必须按 Offer 协商。
- 替代 P-CSCF 的受保护 TCP 来源必须能够送达 ACK。

**验证结果**:

- `go test ./...`：全部通过。
- `npm test`：29 项全部通过。
- `go build`（Linux amd64、`CGO_ENABLED=0`）：通过。
- `git diff --check`：通过。
- 实机 O2 Wi-Fi Calling 注册、拨号、来电接听、双向通话和挂断均已验证。

**安全与仓库约束**:

- 测试数据中的真实 SIM、IMEI、读卡器和号码已替换为合成值。
- 抓包脚本、解密脚本、抓包数据和临时构建产物继续保留本地，不提交 GitHub。
- 本文不包含真实 IMSI、ICCID、IMEI、设备序列号、电话号码、内部 IP 或密钥。

---

### 53. 完善设备删除后的 VoWiFi 生命周期清理

本次在同步上游 `4859412 FIX #181` 时保留了 VoNet 自有的 `VoWiFiDeviceRemover + Remove`
等价实现，没有引入上游的 `Unregister` 双轨接口。

**修改内容**:

- `Remove` 删除 runtime entry 时同步清除 desired enable、自动重试、重连、禁用和
  provisioning 等 pending 意图。
- `runOperations` 在活动操作结束后检查 `manager.entries[deviceID] != item`，防止已经
  删除或替换设备的旧 worker继续执行后续 enable、disable 或 reconnect。
- `StateProjector.Save` 在 runtime 写入失败后确认设备是否已经删除；仅当设备返回
  `store.ErrNotFound` 时忽略迟到状态写入，其他数据库错误继续返回。
- 保留原有 `watchDone` 等待、`WithSIM` provisioning 取消、按需 `Ensure`、重试退避和
  多设备启动错开逻辑。

**测试覆盖**:

- 异常设备和无 AT 口的非 PCSC 设备不会自动建档。
- PCSC reader 没有 AT 口时仍会创建设备和 IMEI Lease。
- PCIe/MHI WWAN 无 AT 口会标记 `at_port_missing`，有 AT 口不会误标记。
- 删除设备会调用现有 `Remove` 生命周期清理。
- 删除设备后的迟到状态保存返回成功，真实数据库错误不会被吞掉。
- 已删除 entry 的 worker只执行当前活动操作，不再继续后续操作。
- `Remove` 会清除 entry 中所有 pending 意图。

---

### 54. 英国 O2 Wi-Fi Calling 短信发送修复与抓包经验

**问题背景**:

- 英国 O2 Wi-Fi Calling 已可稳定注册、拨打电话和接听来电，但移动始发短信持续失败。
- Android 实机在相同 SIM、Wi-Fi 和位置下可以成功发送短信，说明问题位于 VoNet 的 SMS-over-IMS 请求构造或兼容参数，而不是 SIM 未开通短信业务。
- 本次继续采用“Android 成功样本 + VoNet 服务端 SIP/ESP 抓包 + 逐项等价替换”的方式定位，最终成功收到 `202 Accepted` 和对应 RP-ACK。
- 文档只记录协议差异和排查经验，不包含真实 IMSI、ICCID、IMEI、MSISDN、SMSC、内部 IP、设备序列号、接入点标识、密码、密钥或原始抓包内容。

**一、抓包与对照方法**:

1. Android 侧记录 telephony、QImsService、ImsSmsDispatcher、SMS provider 状态，以及 IKE/ESP/XFRM 和 SIP 端口数据。
2. VoNet 侧使用服务端 `strace` 保存完整 SIP MESSAGE 和响应，并保存外层 ESP、XFRM SA、运行日志和 API 结果。
3. 对 Android 成功短信读取发送时间、SMSC、收件号码格式、正文长度和 Qualcomm `messageRef`，再与 VoNet 生成的 RP-DATA/RPDU 逐字节比较。
4. 尝试 NFLOG 抓取内层解密流量时发现设备自带 tcpdump 无权监听 NFLOG group；该限制被记录，但服务端 `strace` 已能提供完整 SIP 明文，不影响根因分析。
5. 所有真实抓包、日志、密钥和本地诊断脚本继续保存在 Git 忽略目录中，不进入版本库。

**二、SMS PDU 层发现**:

1. Android 成功样本使用运营商 SMSC，并将英国号码编码为国内格式；VoNet 此前把带国家码的输入直接编码为国际 TP-DA。
2. O2 Profile 新增 `sms_recipient_mode=uk-national`，仅对 O2 将英国 `+44` 号码转换为国内 `0` 前缀格式；其他运营商保持原输入格式。
3. RP-DATA 结构保持标准移动始发形式：RP-OA 为空、RP-DA 为运营商 SMSC、User Data 为 SMS-SUBMIT TPDU。
4. TPDU 使用 Android 同类请求的 SMS-SUBMIT 参数、TP-MR/RP-Reference 对齐以及短文本 GSM-7 编码，修正后 PDU 长度和字段边界与成功样本一致。

**三、SIP MESSAGE 层发现**:

1. 初期所有 MESSAGE 都被 O2 返回裸 `401 Unauthorized`，响应不含 `WWW-Authenticate`、`Proxy-Authenticate` 或诊断头。这不是普通 Digest challenge，不能通过重复 AKA 计算解决。
2. 已排除并验证无效的组合包括：多种 SMSC/PSI Request-URI、tel/SIP 身份交叉、注册 IMSI/MSISDN 身份交叉、空 Digest、缓存 AKA Digest、`integrity-protected=yes`、无 Authorization、`user=phone` 参数和多种路由目标。
3. MESSAGE 最终收敛为 O2 专用最小头集合：使用 tel 身份、无 Digest、正确 Contact 端口 Via，并省略与 Android 成功路径无关的 Contact、Supported、User-Agent、Content-Transfer-Encoding 等额外头。
4. 其他运营商仍走原标准 MESSAGE 构造；O2 差异全部由 Carrier Profile 开关控制，避免影响现有运营商。

**四、决定性根因**:

1. `P-Access-Network-Info` 是 O2 短信业务位置授权的关键。缺少 PANI 时仍返回裸 `401`。
2. 使用广播或无效 WLAN 占位节点时，O2 会进入业务授权检查并返回：
   `403 Forbidden - Service not allowed in this location`。
3. 使用 `country=GB` 和真实本地管理单播 WLAN 节点标识后，同一条 RP-DATA 收到 `202 Accepted`，随后收到相同事务标识的 RP-ACK，证明 IP-SM-GW 已接收短信提交。
4. 获胜 PANI 形式为：
   `IEEE-802.11;country=GB;i-wlan-node-id=<WLAN-NODE-ID>`。
5. 真实 WLAN 节点标识属于位置相关敏感配置，不写入文档。wificalling JSON 的 `sms_pani` 字段保存完整 PANI；当前仓库和测试使用合成 WLAN 节点标识，不代表真实接入点或地理位置。

**五、代码修改**:

- `internal/vowifi/carrier_profiles.json`、`internal/vowifi/carrier_compat.go`：增加 O2 专用短信号码格式、最小 MESSAGE 头、Contact Via、JSON PANI 配置和 O2 回退策略。
- `internal/vowifi/ims/sms_runtime.go`：对齐国内 TP-DA、tel 身份、最小 SIP MESSAGE、正确 Via、PANI 以及无挑战拒绝的受控回退。
- `internal/vowifi/ims/sms_psi.go`、`internal/pcsc/service.go`：保留 EF_PSISMSC 读取和 SMSC fallback；SIM 不支持 PSI 文件时使用运营商 SMSC 路由。
- 测试中的号码、IMSI、ICCID、接入点标识均使用合成值或保留测试值，不使用真实用户数据。
- 抓包工具和抓包数据不纳入本次代码修改，继续按本地诊断资料保留。

**六、以后修复其他运营商短信的参考顺序**:

1. 先用 Android/原生终端成功发送一条短文本，记录 SMSC、号码格式、正文编码和网络响应。
2. 对比 RP-DATA/RPDU，不要先盲目修改 SIP URI；RP-DATA 字段边界错误会被网络表现成不直观的拒绝。
3. 检查 Request-URI、From、P-Preferred-Identity、Authorization、Via 和 Route，但一次只改变一组变量并记录响应码。
4. 对无 `WWW-Authenticate` 的裸 `401`，不要继续无限计算 Digest；优先检查服务选择、接入网络信息和业务位置授权。
5. 重点检查 `P-Access-Network-Info`、国家码、WLAN 节点格式和本地管理单播约束。位置受限运营商可能用 `403 Service not allowed in this location` 表示 PANI 不可信。
6. 一旦收到 2xx，确认是否继续收到 RP-ACK 或 SMS-STATUS-REPORT，再判断是“网络接收成功”还是“终端已投递”。
7. 获胜参数应固化为运营商 Profile，并用合成号码、合成身份和合成接入点标识写回归测试。
8. 抓包脚本、pcap、strace、XFRM 密钥、logcat、设备标识和位置数据必须保持 Git 忽略；文档只保留占位符和协议结论。

**七、验证结果**:

- O2 实机短信由 Android 成功发送，用于建立参考样本。
- VoNet 使用相同短信语义和号码格式后，O2 返回 `202 Accepted`，并返回 RP-ACK。
- O2 Profile 的国内 TP-DA、tel 身份、最小 MESSAGE 头、Contact Via 和 JSON PANI 均有回归测试。
- 标准运营商 MESSAGE 构造、PANI 和鉴权行为保持不变。
- `go test ./...`、`npm test`、`npm run build` 和 `git diff --check` 通过。

**安全与仓库约束**:

- `UPDATE.md` 不包含任何真实用户、SIM、设备、网络位置、凭据或密钥信息。
- 抓包脚本、解密脚本、抓包数据和构建产物均由 `.gitignore` 覆盖。
- 本次修复不提交 GitHub；PANI 作为 wificalling JSON 的运营商兼容配置保存。

---

### 55. Web 全局来电弹窗与通话状态识别修复

**问题背景**:

- 前端界面重构后，设备通话页面在部分场景下不会以弹窗形式提示来电。
- 排查确认 VoWiFi 和蜂窝接口仍能返回通话记录，问题位于 Web 前端的挂载范围和弹窗架构。
- 原通话组件只存在于 `/call` 路由；用户停留在仪表盘、设备、短信、日志等页面时，通话页面没有挂载，因此不会轮询来电，也不会弹出接听界面。
- UI 重构本身没有删除 `CallPanel` 的轮询逻辑，但原本仅在右栏显示的来电面板没有形成全局弹窗。

**修改内容**:

- 新增 `IncomingCallMonitor` 全局来电监听组件，并挂载到登录后的应用外壳。
- 每 2 秒读取所有运行中设备的通话状态，在仪表盘、设备、IMEI、代理、节点、任务、日志、设置等任意页面显示全局来电 Modal。
- 弹窗使用 body portal，不受当前路由、侧边栏、页面滚动或卡片裁剪影响；显示设备名称、来电号码、接听和拒接操作。
- 接听后自动跳转到对应设备的 `/call?device=<device-id>` 页面继续通话；Call Page 会按 URL 参数选择正确设备。
- 拒接后直接关闭弹窗；接听或拒接后的旧 ringing 状态不会重复弹窗。
- `findIncomingCall` 统一 VoWiFi 和蜂窝通话识别：
  - VoWiFi：`incoming/ringing/waiting`。
  - CLCC 蜂窝：来电方向下的 `state=0/3/4/5`。
  - 同时排除已结束通话和明确的拨出通话，避免拨出振铃被误判为来电。
- Call Page 桌面端也持续轮询通话状态，并在通话结束后正确退出 incoming 状态。

**验证结果**:

- 在仪表盘页面使用合成 `state=4` 来电数据验证，全局弹窗能够在非 `/call` 页面显示。
- 弹窗显示合成号码、设备名称、接听和拒接按钮，不受侧边栏和页面容器影响。
- `npm test`：29 项全部通过。
- `npm run build`：通过。
- `git diff --check`：通过。

**安全与仓库约束**:

- 测试仅使用合成设备 ID 和合成号码，不包含真实 SIM、IMEI、MSISDN 或通话记录。
- 本次仅修改 Web 前端，不修改 Go 后端接口或通话协议逻辑。

---

### 56. 上游同步中的 Bark 多目标与短信策略兼容加固

**修改内容**:

- Bark 支持每个 URL 单独绑定设备，URL key 在界面和错误中脱敏；保存时清理过期和重复 scope，测试通知仍覆盖草稿中的所有 URL。
- Bark 短信、来电、自动任务统一走多目标投递：单个 URL 失败不会阻断其他 URL，错误不包含 Bark URL/key；设置页测试则要求全部目标成功，避免用户误以为配置已全量可用。
- API Token/MCP、eSIM/QMI、AT Profile 和 IKE 安全修复采用 VoNet 命名和本地迁移边界，MCP 默认关闭，未知 USB 探测默认关闭。
- 保留 VoNet 原有短信目标策略：有效号码不做国家封禁，HTTP、Telegram、自动任务和 MCP 共用同一入口，避免同步上游后误拒绝 `+86` 或其他运营商号码。
- 二进制更新完成后使用独立超时上下文原子撤销全部会话和 API Token，客户端立即断开也不会取消撤销；管理员密码轮换继续撤销两类凭据，CLI Token TTL 与 Web/API 统一限制为最长 8760 小时。
- card-policy reconciler 的身份不匹配和热插拔重连仅允许 `idle/failed` 阶段，避免打断 `sim_ready/access_ready/tunnel_ready/ims_ready/sms_ready`。
- MCP 短信仍走蜂窝 AT 路径且不写网页会话历史；README 明确该差异以及只读 `GET /api/sms/thread` 的已读副作用。

**验证结果**:

- `go test ./...`、`go vet ./...`、`npm test`（31 项）、`npm run build` 均通过。
- Linux amd64、arm64、armv7 交叉编译通过。
- 仅使用合成号码、合成 Token 和合成设备 ID 进行测试，不包含真实 SIM、IMEI、MSISDN、Bark key、密码或抓包数据。

---

### 57. AT host data、ZXIC 与 SSO 上游同步兼容加固

**修改内容**:

- 增加 AT PDP 主机数据、BusyBox `udhcpc` 租约、IPv4/fwmark 策略路由、AT/QMI 双后端切换和崩溃恢复清理。
- 保留并扩展本地设备能力：HardwareKind 优先、PCSC IMEI Lease、DJI/Baiwang、DITO/CTEXCEL/O2、eSIM/MBN、自动任务和设备删除生命周期。
- 引入共享 AT core 与验证版 ZXIC adapter；EC20 兼容入口、错误别名、`ExecuteAPDU`、ISIM strict、HomePLMN 和 RF fail-closed 策略保持不变。
- adapter binding、checkpoint、physical resolver 和 runtime data session 使用 deviceID/USB generation/ICCID 隔离，避免同 ICCID 多设备和热切换混用。
- 引入 SSO/OIDC/JWKS 与 Pocket ID、GitHub、Generic OAuth2；migration 使用 VoNet 34，保留 API Token/MCP/只读权限和密码登录。
- 增加 credential epoch、SSO flow 撤销、安全 redirect、`go-jose` 4.1.7 和浏览器/服务器共享安全向量。
- 前端增量加入 SSO 设置/登录和 ZXIC 提示，保留重构 UI、全局来电弹窗、`MODEM · PC/SC · PANEL` 和 SMS 限流大于等于 1。

**验证结果**:

- `go test ./...`、`go vet ./...`、`npm test`（47 项）、`npm run build` 通过。
- `npm run test:e2e`（7 项）和 SSO Docker Compose 配置检查通过。
- Linux amd64、arm64、armv7 交叉编译通过。
- `git diff --check` 通过。

**安全与部署约束**:

- AT host data 需要 Linux、iproute2、BusyBox `udhcpc`、接口独占和网络权限。
- SSO 需要可信 HTTPS IdP、`VONET_SECURE_COOKIES=true`、根路径回调和敏感数据库备份保护。
- schema 34 无自动降级；升级前必须备份数据库/WAL/TLS/插件/环境文件。

---

### 59. 上游 17 次提交最终对比与回滚/发布加固

**修改内容**:

- 对 `VoCat-reference` 的 `384a447..70b0e69` 共 17 次提交、42 个文件逐文件复核。SSO/OIDC、Cookie 传输策略、IPv6/private origin、CSRF、API Token 和迁移测试均已同步，没有覆盖或删除 HardwareKind、PCSC IMEI、DJI/Baiwang、DITO/CTEXCEL/O2、VoWiFi、通话、SMS、代理、节点转换、全局来电弹窗或现有 UI 自研功能。
- VoNet schema 映射保持 migration 33 API Token、migration 34 SSO/credential epoch，并新增 migration 35，仅为 `sessions` 添加 `secure_cookies`。CreateSession、Secure policy、CSRF 替换和 API Token 创建均在事务中绑定当前 credential epoch。
- 新增 `auth-cleanup --no-migrate` 和 `store.OpenWithoutMigration`。恢复 schema 34 快照后可在不升级数据库的前提下撤销 Session、API Token 和 pending SSO flow，同时保留 SSO 配置与外部 binding，使旧二进制仍可启动。
- 补充 Session/API Token 创建、`DeleteAllAuthCredentials`、清理事务回滚和并发 CSRF 轮换的回归测试；Secure Cookie 只能单向升级，不能回退。
- tag release 增加 `npm test`、Playwright Chromium 安装和 `npm run test:e2e`；SSO CI 覆盖 `v*` tag。Docker amd64/arm64 smoke 增加完整迁移与 schema 34 WAL/SHM 快照的两阶段验证。
- 修正 README、多语言文档和回滚说明：schema 34 快照必须使用 `auth-cleanup --no-migrate` 或匹配的旧二进制清理，禁止先用普通 `auth-cleanup` 升级到 schema 35 后再启动旧版本。

**验证结果**:

- `go test ./internal/store ./cmd/vonet`、`go vet ./...` 通过；凭据与 CSRF 并发专项重复 10 轮通过。
- `npm test` 49/49，`npm run test:e2e` 11/11，`npm run build` 通过。
- `auth_restore_smoke.py` 的 schema 34 WAL/SHM 两阶段 smoke、Python 编译、Compose config、actionlint 和 `git diff --check` 通过。
- 全量 `go test ./...` 仅 `internal/exportproxy` 两项因 Windows 系统保留 TCP 1080 端口失败，其余包通过；`-race` 因本机缺少可用的 CGO C 编译器未执行。

**部署与验证约束**:

- 本机 Docker daemon 不可用，未在本地实际构建/运行 amd64/arm64 双架构镜像；CI 已配置对应 `--platform` matrix 执行相同的迁移、恢复和清理断言。
- 上游新增 Cookie/迁移测试文件和 `scripts/ci/auth_restore_smoke.py` 必须随代码一并提交，后续提交不得遗漏未跟踪文件。

---

### 60. 离线代理绑定、eSIM Profile 缓存与登录会话竞态修复

**问题背景**:

- VoWiFi 注册或 IKE 失败后，设备运行状态可能临时变为离线，代理中心错误地把“设备在线”当成“允许保存代理绑定”的前置条件，导致 `Reader-*` 等设备无法选择“绑定代理”。
- 设备离线或 Profile 读取失败后，前端内存缓存会丢失最后已知的 eSIM Profile；页面刷新后未绑定的离线 Profile 无法进入绑定列表。
- 登录接口已经返回 `auth.login success`，但启动阶段尚未完成的 session/preferences 请求稍后返回 401，覆盖新登录状态并把用户送回登录页。

**修改内容**:

- 代理绑定模型新增 `bindingEligible`，与 `online/physicalPresent/controlOnline/vowifiReady` 分离；只要设备 ID、ICCID/Profile 身份可信，就允许保存、编辑和删除代理路由。
- 代理绑定列表、Profile 选择器和路由编辑抽屉统一使用绑定资格作为操作门禁；离线保存成功后明确提示将在下次 VoWiFi 启动或重连时应用。
- 代理绑定写接口返回兼容的 `reconnect_requested/reconnect_error`，保存或删除已成功但立即重连失败时不会误报保存失败；API 只返回统一安全提示，详细错误保留在服务端日志。
- 新增服务端 last-known eSIM Profile 缓存，使用敏感 `app_settings` 保存，不增加数据库 migration；缓存仅保留 ICCID、Profile 名称、运营商、状态和分类等白名单字段，剥离 EID、AID 和未知字段。
- Profile 缓存加入稳定硬件身份和 USB generation 校验；无稳定硬件标识时拒绝缓存，设备 ID/读卡器更换不会复用旧 Profile。无效、损坏、版本不匹配或硬件身份不一致的缓存会在读取时清理。
- `ErrNoEUICC` 连续三次确认后才清理缓存，避免瞬时 PCSC/eUICC 误报删除最后可用 Profile；Profile 切换、重命名、禁用、删除和下载前先成功失效缓存，清理失败会阻止硬件操作。
- `/devices/{id}/esim?cached=1` 和 `/esim/profiles?cached=1` 支持设备离线或读取临时失败时返回 stale 缓存；普通请求仍保持实时语义。
- eSIM 页面显示缓存状态，缓存/stale 时禁用切换、改名、删除、策略和下载；状态变化会取消已打开的编辑、删除弹窗并中止下载，所有回调执行前再次校验实时状态。
- 密码登录以后端 `auth.login` 响应为认证结果，不再由后续 `/auth/session` 决定登录成败；登录响应中的嵌套 `user` 身份统一映射。
- 401 事件携带请求开始时间，启动阶段的旧 session/preferences 请求不能覆盖新登录；认证代次同时阻止旧 refresh 结果覆盖新状态。

**验证结果**:

- `npm test` 66/66，`npm run build`，`npm run test:e2e` 11/11 通过。
- `go test ./internal/server`、`go vet ./...` 和 `git diff --check` 通过。
- 新增测试覆盖离线绑定、缓存 Profile、硬件身份/generation、缓存清理重试、Profile 操作失效、API envelope、eSIM 操作门禁和旧 401 登录竞态。

**安全与部署约束**:

- Profile 缓存位于 VoNet 数据库的敏感 `app_settings` 中，仍需像数据库、WAL、备份和 TLS 材料一样受控。
- 本机未重新执行全量 `go test ./...`；此前仅 `internal/exportproxy` 因 Windows 系统保留 TCP 1080 端口失败，与本次修改无关。
- 需要重新构建并部署包含新前端资源的 VoNet 二进制后再进行实机登录、代理绑定和 eSIM 离线缓存验证。

---

### 61. SIM 卡管理、PC/SC VoWiFi 与卡片原子恢复

**问题背景**:

- 本次本地开发在第 60 条之后继续扩展了 SIM 卡管理，并修复了 USB SIM 读卡器、VoWiFi 和卡片生命周期中的多处问题。
- 读卡器没有传统蜂窝 AT 口，但仍被通用硬件操作错误地按 AT modem 处理，产生 `device has no usable AT port` 日志，界面也错误显示 AT 终端和 AT 端口。
- 卡片概览、卡策略、设备状态使用了不同的在线判定来源，导致“设备在线”但 USSD/卡策略仍被禁用。
- 删除卡片会触发旧 VoWiFi 生命周期停止操作；如果重新导入只恢复卡片记录、不恢复卡策略，VoWiFi 会停留在 `disabled` 或 `stopping`。
- eSIM Profile 卡片的绑定协调每次刷新都重新读取 eSIM Inventory，导致 PC/SC 读卡器持续访问卡片，而不是只在身份缺失或变化时读取。
- 卡片列表中的完整 ICCID 暴露了不必要的敏感信息，导入如果不是原子操作，中途失败还会留下部分卡片。

**主要修改内容**:

1. **SIM 卡数据与原子导入**
   - 新增 `sim_cards`、卡片绑定、观察记录、卡策略和 APN 等本地数据支持。
   - 新增 `/api/cards/import` 批量导入接口；一次请求只提交整批卡片配置。
   - 导出文件只包含 `ICCID`、`卡片名称`、`卡片类型`、`备注` 四个字段。
   - 导入先校验整批数据，再在同一个 SQLite 事务中创建或更新；任一记录失败都会回滚整批。
   - 已有卡片只替换名称、类型和备注，不覆盖绑定、策略、IMSI、EID/AID 或其他元数据。
   - 导入缺失策略时在同一事务内创建安全默认策略，避免删除再导入后出现“缺少策略”。

2. **卡片策略与生命周期恢复**
   - PC/SC 读卡器策略固定为 `PCSC + VoWiFi + 飞行模式`，不允许蜂窝驻网或 AT 数据路径。
   - 读卡器缺少卡策略时，策略协调器会自动创建固定策略并请求 VoWiFi 恢复，而不是永久暂停。
   - 删除、重新导入、换卡和绑定状态切换后，VoWiFi desired state 会在当前停止任务结束后自动继续启用。
   - VoWiFi 重连请求固定携带当前 ICCID，避免旧绑定存在时返回 409 或重连到错误卡片。

3. **PC/SC 读卡器边界**
   - PC/SC 读卡器不再暴露 AT 终端、AT 端口或 AT 相关配置。
   - 通用 modem 控制操作对 PC/SC 设备返回“不支持该能力”，不再产生误导性的 AT 端口错误。
   - 预期的 PC/SC 能力拒绝不再写入硬件失败日志。
   - eSIM EID/Profile AID 首次解析后持久化，后续绑定协调复用已保存身份；只有身份缺失、变化或不匹配时才读取 eSIM。
   - 连续策略协调只读取一次 eSIM Inventory，避免读卡器持续闪烁/持续访问卡片。

4. **SIM 管理前端**
   - 新增独立的“卡片管理”页面和卡片详情标签：概览、卡片配置、设备配置、卡策略、AT、USSD。
   - 卡片配置支持修改名称、卡片类型和备注，ICCID 创建后只读。
   - 增加一键导入卡片和一键导出卡片；导入前确认，成功后显示新增/更新数量。
   - 卡片列表采用紧凑 SIM 卡卡片布局，状态点位于“当前启用”左侧，在线和策略执行中统一使用绿色。
   - ICCID 默认部分脱敏，提供统一的敏感信息显示/隐藏按钮。
   - 当前卡片增加短信和通话按钮，按 ICCID 跳转并自动选中对应卡片。
   - USSD 入口按卡片在线状态开放；VoWiFi 卡片使用 IMS USSI，不再错误显示蜂窝动画或禁用入口。

5. **概览与仪表盘**
   - 概览恢复旧版“SIM / 设备”面板，展示 IMEI、ICCID、IMSI、本机号码、原运营商、固件版本、飞行模式、运行模式、ME、SM。
   - 读卡器概览优先显示 VoWiFi 状态，不因启动阶段的 runtime 状态短暂为 false 而切回蜂窝动画。
   - 仪表盘拆分为设备列表和卡片列表：设备卡只显示设备/硬件信息，卡片卡显示 VoWiFi 或蜂窝数据状态。
   - 模块在线率只使用设备数据计算，卡片列表不会改变模块在线率。
   - 修复设备列表按钮覆盖下一项信息的问题，并调整设备项间距。

**测试与验证**:

- 新增原子导入回滚测试：第二条记录失败时，第一条卡片及其策略也不会保留。
- 新增导入默认策略测试、已有策略保留测试和 eSIM 身份只读取一次测试。
- 新增 PC/SC 通用操作不暴露 AT 错误、VoWiFi/USSI 路由、卡片配置、导入导出、敏感信息遮罩和仪表盘分类测试。
- `go test ./internal/store`、`go test ./cmd/vonet` 通过。
- 前端 `npm test` 79 项通过，`npm run build` 和 TypeScript 检查通过。

**上游同步注意事项**:

- 下列本地文件包含本次新增功能和修复，合并上游时必须保留本地版本或逐项人工合并，不能直接覆盖：
  - `internal/store/cards.go`、`internal/store/cards_test.go`、`internal/store/migrations.go`
  - `internal/server/card_api.go`、`internal/server/card_gate.go`、`internal/server/device_api.go`
  - `cmd/vonet/card_policy_reconciler.go`、`cmd/vonet/card_policy_reconcile_test.go`
  - `internal/device/manager.go`、`internal/device/data*.go`
  - `web/src/pages/CardsPage.tsx`、`web/src/components/cards/`
  - `web/src/components/dashboard/`、`web/src/components/devices/`
  - `web/src/pages/DashboardPage.tsx`、`web/src/pages/DevicesPage.tsx`
- 特别保留：原子导入、导入默认策略、PC/SC AT 屏蔽、eSIM 身份缓存、VoWiFi 重连 ICCID 绑定、敏感 ICCID 遮罩和设备/卡片仪表盘分类。
- 上游若修改 `internal/store/migrations.go`、`device_api.go` 或 `CardOverviewTab.tsx`，请优先合并本条第 61 条描述的行为，再合并上游 UI/数据库变更。

**安全与部署约束**:

- 导出的卡片配置包含 ICCID 和用户备注，必须作为敏感文件保存；界面默认只显示部分 ICCID。
- 本次改动不修改真实 SIM、IMSI、设备序列号、PANI、密钥或证书配置；文档仅记录功能和行为。
- 部署包含新前端资源的 VoNet 二进制后，需要重新验证删除卡片、一键导入、VoWiFi 恢复和 PC/SC 读卡器读取频率。

---
