# VoNet 上游同步日志

本文档记录每次从上游 VoNet-reference 同步版本更新的详细分析和操作记录。

---

## 当前同步状态

- **本地基准版本**: 4da2cbc (fix(device): recover EC20 after cold boot and USB reset)
- **已同步版本**: 96a1233 (fix(device): recover EC20 from prolonged network search)
- **已同步版本**: 4760e33 (fix(workflows): update action versions and add retry script for BuildKit image pull)
- **已同步版本**: b8f4c0c (fix(device): allow SMS storage index zero)
- **已同步版本**: 8f18f45 (fix(sms): clear modem storage after a successful sync)
- **已同步版本**: fd14902 (Fix/ims ipsec mss)

- **已同步版本**: 621be85 (feat: implement authentication service and settings API)
- **已同步版本**: 39b8a53 (fix(ims): hide SIM data download messages from inbox #131)
- **已同步版本**: 537ecae (fix(ims): reply RP-ACK for SIM data download when UICC handler is not set #132)
- **已同步版本**: 84151f4 (feat(device): add per-card MBN profile override #133)
- **已同步版本**: 484cd23 (fix(vowifi): support DITO roaming subscriber identities #136)
- **已同步版本**: 12ce8f0 (feat(sms): decode WAP Push MMS notification subjects #141)
- **已同步版本**: 2d023a7 (Delete docs/E911.zh-CN.md；本地有意保留该文档)
- **已同步版本**: 4859412 (FIX #181；选择性同步，保留 VoNet 本地等价实现)
- **已同步版本**: f979e60 (feat(notifications): bind each Bark URL to its own devices)
- **已同步版本**: a6c0c75 (feat: API Token 认证与 MCP 端点)
- **已同步版本**: 635e84f (fix(auth): enforce read-only access and coordinate MCP eSIM switches)
- **已同步版本**: 33ecd4c (fix(device): isolate eSIM recovery and supervise QMI proxy)
- **已同步版本**: 01087de (feat(modem): add typed AT capability profiles and unknown-USB discovery)
- **已同步版本**: d1968d1 (fix(ike): sanitize SOCKS5 relay lookup failures)
---

## 待同步提交列表（96a1233 → 4760e33）

| # | Commit | 描述 | 冲突风险 | 说明 |
|---|--------|------|---------|------|
| 1 | e7bbdef | fix(vowifi): preserve fatal IKE notifications during EAP | ⚠️ 中等 | provider.go 有本地修改 |
| 2 | 152da09 | FIX #120: MT SMS RP-ACK returns 403 with wrong IMS identity | ✅ 无冲突 | sms_runtime.go 无本地修改 |
| 3 | aa00c32 | Update README | ✅ 无冲突 | README 仅有格式差异 |
| 4 | 4760e33 | fix(workflows): update action versions and add retry script | ✅ 无冲突 | CI 文件无本地修改 |

---

## 文件与上游基准对比结果

| 文件 | 与上游基准 96a1233 对比 | 说明 |
|------|----------------------|------|
| internal/vowifi/ike/provider.go | ⚠️ 有本地修改（9行） | 修改 #6：EAP 错误诊断 + rejectFatalNotifications |
| internal/vowifi/ike/provider_test.go | ✅ 完全一致 | 可直接应用上游变更 |
| internal/vowifi/ike/wire.go | ✅ 完全一致 | 可直接应用上游变更 |
| internal/vowifi/ims/sms_runtime.go | ✅ 完全一致 | 可直接应用上游变更 |
| internal/vowifi/ims/message_identity.go | 新增文件 | 本地不存在 |
| internal/vowifi/ims/message_identity_test.go | 新增文件 | 本地不存在 |
| README.md | ✅ 格式差异（18行） | 不影响功能 |
| .github/workflows/docker.yml | ✅ 完全一致 | 可直接应用上游变更 |
| .github/workflows/release.yml | ✅ 完全一致 | 可直接应用上游变更 |
| scripts/ci-pull-image.sh | 新增文件 | 本地不存在 |

---

## 详细分析

### 1. e7bbdef - fix(vowifi): preserve fatal IKE notifications during EAP

**上游变更**:
- wire.go：新增 const notifyAuthenticationFailed = 24
- provider.go：
    - EAP 循环顶部新增 ejectFatalNotifications(currentPayloads)（在 onePayload 前）
    - ejectFatalNotifications() 新增
      otifyAuthenticationFailed case → 返回 ErrEAPAuthenticationRejected
- provider_test.go：新增 TestRejectFatalNotificationsClassifiesAuthenticationFailed

**本地 provider.go 的3处差异（vs 上游基准 96a1233）**:
1. 空行格式差异（无影响）
2. 修改 #6：onePayload 错误时附加 payload 类型诊断信息
3. 修改 #6：sendAndReceiveIKEPayloads 后添加 ejectFatalNotifications 检查

**合并方案**:
1. ✅ wire.go：添加
   otifyAuthenticationFailed = 24（纯新增，文件与基准一致）
2. ✅ provider_test.go：添加测试（纯新增，文件与基准一致）
3. ✅ ejectFatalNotifications() 函数：添加 Notify 24 case（纯新增 switch case，无冲突）
4. ⚠️ **EAP 循环顶部添加 ejectFatalNotifications 检查**（与本地已有的底部检查互补）

**EAP 循环合并后的结构**:
`
for round := 0; round < 10; round++ {
// [新增] 上游 e7bbdef：首轮 Notify 错误检查
rejectFatalNotifications(currentPayloads)

    // [本地修改 #6] payload 诊断
    eapPayload, err := onePayload(currentPayloads, payloadEAP)
    if err != nil { ... payloadTypes 诊断 ... }

    aka.handle()
    sendAndReceiveIKEPayloads()

    // [本地修改 #6] 后续轮次 Notify 错误检查（保留）
    rejectFatalNotifications(currentPayloads)

    if round == 9 { ... }
}
`

**两个检查位置的关系**:
- 顶部检查：捕获**首轮**响应或**上一轮 send/receive 返回的** Notify 错误，在 onePayload 解析前就拦截
- 底部检查：捕获**当前轮 send/receive 返回的** Notify 错误，更早报告（不用等到下一轮迭代顶部）
- 两者互补，保留底部检查不影响正确性

---

### 2. 152da09 - FIX #120: MT SMS RP-ACK returns 403 with wrong IMS identity

**上游变更**:
- message_identity.go：新增文件，实现 messagePublicIdentity() 函数
- message_identity_test.go：新增文件，完整单元测试
- sms_runtime.go：
    - 新增 sendSIPMessageWithIdentity() 函数（7参数，含 preferredIdentity）
    - sendSIPMessageWith() 重构为调用 sendSIPMessageWithIdentity 的包装（签名不变）
    - sendDeliveryReport() 改用 sendSIPMessageWithIdentity 并传入 P-Called-Party-ID
    - From/P-Preferred-Identity 改用 messagePublicIdentity() 选择的身份

**关键确认**: sms_runtime.go 与上游基准**完全一致**，无任何本地修改。上游变更可直接应用。

**依赖检查**:
- messagePublicIdentity() 依赖 splitHeaderValues() → 已存在于本地 message.go:241
- session.evidence.AssociatedIdentities → 已存在于本地 	ypes.go:259
- session.identity.public → 已存在于本地 provider.go:389
- sendSIPMessageWith 签名不变（6参数）→ 所有调用者（SendSMS、USSI）不受影响

**合并方案**:
1. ✅ 新增 message_identity.go（纯新增文件）
2. ✅ 新增 message_identity_test.go（纯新增文件）
3. ✅ sms_runtime.go：直接应用上游变更（无本地修改冲突）

---

### 3. aa00c32 - Update README

**上游变更**: 捐赠地址更新 + 多语言文档更新

**本地差异**: README 有18行格式差异（构建说明等），不影响功能

**合并方案**: 更新捐赠地址，保留本地格式差异

---

### 4. 4760e33 - fix(workflows): update action versions and add retry script

**上游变更**:
- docker.yml：Actions 版本更新（checkout v4→v6, buildx v3→v4 等）
- elease.yml：Actions 版本更新
- ci-pull-image.sh：新增 BuildKit 镜像拉取重试脚本

**本地状态**: 两个 workflow 文件与上游基准完全一致

**合并方案**: 直接覆盖 + 新增脚本文件

---

## 合并操作计划

### 第一阶段：纯新增文件（无风险）
1. 新增 internal/vowifi/ims/message_identity.go
2. 新增 internal/vowifi/ims/message_identity_test.go
3. 新增 scripts/ci-pull-image.sh

### 第二阶段：无本地修改的文件（直接应用上游变更）
4. internal/vowifi/ims/sms_runtime.go：直接应用上游变更（无冲突）
5. internal/vowifi/ike/wire.go：添加
   otifyAuthenticationFailed = 24
6. internal/vowifi/ike/provider_test.go：添加测试

### 第三阶段：有本地修改的文件（需手动合并）
7. internal/vowifi/ike/provider.go：
    - 在 ejectFatalNotifications() 中添加 Notify 24 case
    - 在 EAP 循环顶部添加 ejectFatalNotifications 检查（与本地底部检查互补）
    - 保留本地的 payload 类型诊断和底部检查

### 第四阶段：文档和 CI（低风险）
8. README.md：更新捐赠地址
9. .github/workflows/docker.yml：直接应用上游变更
10. .github/workflows/release.yml：直接应用上游变更

---

## 之前分析的修正

1. **修正**: 之前说"sms_runtime.go 有本地修改" → **错误**，实际与上游基准完全一致
2. **修正**: 之前说"EAP 循环顶部检查不需要添加" → **错误**，需要添加以捕获首轮 Notify 错误
3. **确认**: 底部 ejectFatalNotifications 检查应保留，与顶部检查互补

---

## 同步操作记录

### 2026-09-06 同步上游 96a1233 → 4760e33

**操作内容**:

| 阶段 | 文件 | 操作 | 结果 |
|------|------|------|------|
| 1 | internal/vowifi/ims/message_identity.go | 新增 | ✅ |
| 1 | internal/vowifi/ims/message_identity_test.go | 新增 | ✅ |
| 1 | scripts/ci-pull-image.sh | 新增 | ✅ |
| 2 | internal/vowifi/ims/sms_runtime.go | 直接覆盖 | ✅ |
| 2 | internal/vowifi/ike/wire.go | 直接覆盖 | ✅ |
| 2 | internal/vowifi/ike/provider_test.go | 直接覆盖 | ✅ |
| 3 | internal/vowifi/ike/provider.go | 手动合并3处 | ✅ |
| 4 | README.md | 更新捐赠地址 | ✅ |
| 4 | .github/workflows/docker.yml | 直接覆盖 | ✅ |
| 4 | .github/workflows/release.yml | 直接覆盖 | ✅ |
| 4 | docs/README.*.md | 直接覆盖 | ✅ |

**provider.go 手动合并详情**:
1. 保留本地 #6 的 payload 类型诊断（onePayload 错误时附加 payloadTypes）
2. 保留本地 #6 的底部 rejectFatalNotifications 检查（sendAndReceiveIKEPayloads 后）
3. 新增上游 e7bbdef 的顶部 rejectFatalNotifications 检查（onePayload 前）
4. 新增上游 e7bbdef 的 notifyAuthenticationFailed(24) case

**编译**: ✅ go build ./...
**测试**: ✅ go test ./internal/vowifi/... -count=1（5个包全部通过）

### 2026-09-09 同步上游 4760e33 → b8f4c0c（三次提交）

**提交列表**:

| Commit | 描述 | 合并方式 |
|--------|------|---------|
| fbe2119 | fix(vowifi): avoid single-fragment SKF payloads | 直接合入 |
| 7dd4746 | fix(ims): 修复短信收发中的注册和身份使用问题 | 手动合并 |
| b8f4c0c | fix(device): allow SMS storage index zero | 直接合入 |

**操作内容**:

| 文件 | 操作 | 说明 |
|------|------|------|
| internal/vowifi/ike/crypto.go | 手动合并 | fbe2119：单fragment时直接调用encryptPayloads，不走SKF分片路径 |
| internal/device/sms.go | 直接合入 | b8f4c0c：index <= 0 → index < 0，允许index=0 |
| internal/vowifi/ims/provider.go | 手动合并6处 | 7dd4746：见下方详情 |
| internal/vowifi/ims/message_identity.go | 追加函数 | 7dd4746：新增originatingSMSPublicIdentity、temporaryPublicIdentityKey |
| internal/vowifi/ims/sms_runtime.go | 手动合并4处 | 7dd4746：见下方详情 |
| internal/vowifi/ims/sms_psi.go | 新增文件 | 7dd4746：smsTarget、validSMSPSI、smsCenterPSIReader接口 |
| internal/vowifi/ec20_sms_psi.go | 新增文件 | 7dd4746：EC20ReadSMSCenterPSI（读取EF_PSISMSC） |
| internal/vowifi/ims/sms_psi_test.go | 新增文件 | 7dd4746：PSI验证、路由、取消等测试 |
| internal/vowifi/ec20_adapter_test.go | 追加测试 | 7dd4746：PSI读取测试 |
| internal/vowifi/ims/sms_runtime_test.go | 手动合并 | 7dd4746：添加ReadSMSCenterPSI方法、P-Associated-URI |
| internal/vowifi/ims/message_identity_test.go | 追加测试 | 7dd4746：MO身份来源、临时身份策略测试 |
| internal/vowifi/ims/provider_test.go | 追加测试 | 7dd4746：stableInstanceUUID测试 |
| internal/vowifi/ims/security_provider_test.go | 追加测试 | 7dd4746：受保护重注册保留Authorization身份 |

**provider.go 手动合并详情**:
1. identitySet结构体新增temporaryPublic字段
2. deriveIdentities返回值添加	emporaryPublic: config.PublicIdentity == ""
3. newSession改用stableInstanceUUID(request)替代randomUUID()
4. 新增stableInstanceUUID函数（UUIDv5，基于IMEI/DeviceID/IMSI派生稳定UUID）
5. applyRegistrationEvidence改用registeredContact计算expires（而非所有contacts）
6. buildRegister：受保护重注册时emptyDigestAuthorization替换integrity-protected=no为=yes
7. 新增crypto/sha1导入

**sms_runtime.go 手动合并详情**:
1. SendSMS入口添加ctx.Err()取消检查
2. ReadSMSCenter后添加context取消/超时错误检查
3. PSI行替换为session.smsTarget(ctx, smsc)调用 + 临时身份preflight检查
4. for循环内添加ctx.Err()取消检查
5. sendSIPMessageWithIdentity：临时身份+SMS+非回复时使用originatingSMSPublicIdentity

**与UPDATE.md本地修改的兼容性确认**:
- ✅ 修改#1-#5（HardwareKind、状态重置、随机IMEI、诊断、UI修复）：无冲突
- ✅ 修改#6（WiFi Calling失败日志）：无冲突，provider.go改动区域不重叠
- ✅ 修改#7（读卡器AT PORT修复）：无冲突
- ✅ 修改#8（README）：无冲突
- ✅ 修改#9（SOCKS5 UDP ASSOCIATE）：无冲突

**编译**: ✅ go build ./...
**测试**: ✅ go test ./internal/vowifi/... -count=1（5个包全部通过）
---

### 2026-09-11 同步上游 b8f4c0c → 8f18f45（fix(sms): clear modem storage after a successful sync）

**提交**: 8f18f45

**提交描述**:
SMS 同步成功后自动删除模组 SM/ME 存储中的已同步短信，防止小容量 ME 存储（约20格）
被占满导致无法接收新短信。默认开启，可通过设置页面关闭。

**操作内容**:

| 阶段 | 文件 | 操作 | 说明 |
|------|------|------|------|
| 1 | internal/device/types.go | 直接覆盖 | 新增 SMSStorageArea/SMSStorageUsage/SMSListing 类型 |
| 1 | web/src/components/settings/SMSAutoClearCard.tsx | 新增 | 自动清理模组短信存储设置卡片 |
| 2 | internal/device/sms.go | 直接覆盖 | ListSMS 返回 SMSListing + parseSelectedCPMSUsage + AT+CPMS 恢复 |
| 2 | internal/device/sms_test.go | 直接覆盖 | 适配新返回类型 + TestParseSelectedCPMSUsage |
| 2 | internal/developer/settings.go | 直接覆盖 | 新增 AutoClearModemStorage/SetAutoClearModemStorage |
| 2 | internal/developer/settings_test.go | 直接覆盖 | 新增 TestAutoClearModemStorage |
| 2 | internal/server/developer_settings.go | 直接覆盖 | 新增 auto_clear_modem_storage 字段处理 |
| 2 | internal/server/developer_settings_test.go | 直接覆盖 | 新增测试 |
| 2 | internal/server/settings_api.go | 直接覆盖 | 新增 settings/sms 路由 |
| 3 | internal/server/server.go | 手动合并3处 | 保留本地 usbPathConflicts + auditCategory，追加 smsStorageMu/smsStorage |
| 3 | internal/server/sms_api.go | 手动合并 | 用上游版本覆盖，恢复本地 blockedSMSDestination(return false) |
| 3 | internal/server/sms_api_test.go | 手动合并 | 用上游版本覆盖，恢复本地 blocked 期望为 false |
| 3 | internal/server/region_test.go | 手动合并 | 用上游版本覆盖，恢复本地 service_blocked 期望为 false |
| 3 | internal/server/device_api.go | 手动合并2处 | ListSMS 接口类型 SMSMessage->SMSListing + sms_storage 插入 |
| 4 | web/src/components/devices/OverviewSimPanel.tsx | 直接覆盖 | ME/SM 存储使用量显示 + 满载警告 |
| 4 | web/src/lib/i18n-en.ts | 直接覆盖 | 新增 SMS 存储相关英文翻译 |
| 4 | web/src/pages/SettingsPage.tsx | 直接覆盖 | 新增 SMSAutoClearCard 组件 |
| 4 | web/src/types.ts | 直接覆盖 | 新增 SMSStorageArea/SMSStorageUsage/SMSSettings 类型 |

**手动合并详情**:

1. **server.go**: 保留本地 usbPathConflicts 字段（DJI fix #32）+ auditCategory 扩展；追加 smsStorageMu/smsStorage 字段和初始化

2. **sms_api.go**: 用上游版本覆盖（含 syncModemSMS autoClear 逻辑、deleteModemSMS 适配、新函数）；恢复本地 blockedSMSDestination 为 return false（禁用中国号码拦截）

3. **sms_api_test.go**: 用上游版本覆盖（含 SMSListing mock 适配、新测试）；恢复本地 TestBlockedSMSDestination 期望为 false

4. **region_test.go**: 用上游版本覆盖（含 ListSMS mock 适配）；恢复本地 service_blocked 期望为 false

5. **device_api.go**: ListSMS 接口返回类型 SMSMessage->SMSListing；configuredDeviceSummary 中插入 sms_storage

**编译**: go build ./... 通过
**测试**: go test ./internal/device/... ./internal/developer/... 通过
**已知失败**: internal/server SendDTMF mock（预先存在，与本次同步无关）

### 2026-09-13 同步上游 8f18f45 → fd14902（Fix/ims ipsec mss）

**提交**: fd14902

**提交描述**:
部分系统因 IPv6 IPsec 路径上 TCP MSS 过大导致 WiFi Calling 注册失败。
新增可选 MTU 兼容模式，对受保护的 TCP 连接设置保守的 MSS (1100字节)。
默认关闭，可在设置页面开启。

**操作内容**:

| 阶段 | 文件 | 操作 | 说明 |
|------|------|------|------|
| 1 | internal/server/vowifi_settings.go | 新增 | VoWiFi 设置 API handler |
| 1 | internal/vowifi/ims/tcp_linux.go | 新增 | Linux TCP MSS 配置（TCP_MAXSEG=1100） |
| 1 | internal/vowifi/ims/tcp_other.go | 新增 | 非 Linux 平台空实现 |
| 1 | internal/vowifisettings/settings.go | 新增 | MTU 兼容设置存储（AppSetting） |
| 1 | web/src/components/settings/VoWiFiMTUCard.tsx | 新增 | 前端 MTU 兼容模式开关卡片 |
| 2 | internal/server/settings_api.go | 直接覆盖 | 新增 settings/vowifi 路由 |
| 2 | internal/vowifi/ims/provider.go | 直接覆盖 | Config 新增 MTUCompatibility 回调 + dialSIP 增加参数 |
| 2 | internal/vowifi/ims/security.go | 直接覆盖 | activateIPSec 传递 mtuCompatibility 给 dialSIP |
| 2 | web/src/lib/i18n-en.ts | 直接覆盖 | 新增6条 MTU 相关英文翻译 |
| 2 | web/src/pages/SettingsPage.tsx | 直接覆盖 | import VoWiFiMTUCard + 插入组件 |
| 3 | cmd/vonet/main.go | 手动合并2处 | 新增 vowifisettings import + ims.Config 添加 MTUCompatibility 回调 |

**手动合并详情**:
1. import 区域：在 vowifiruntime 之后添加 "vonet/internal/vowifisettings"
2. newVoWiFiOrchestrator 函数：在 ims.Config 的 Logger 之前插入 MTUCompatibility 回调
   （函数签名已有 database *store.Store 参数，无需额外修改）

**与UPDATE.md本地修改的兼容性确认**:
- ✅ 修改#1-#5（HardwareKind、状态重置、随机IMEI、诊断、UI修复）：无冲突
- ✅ 修改#6（WiFi Calling失败日志）：无冲突，provider.go改动区域不重叠
- ✅ 修改#7（读卡器AT PORT修复）：无冲突，main.go改动在不同函数内
- ✅ 修改#9（SOCKS5 UDP ASSOCIATE）：无冲突
- ✅ 修改#10-#36（节点转换相关全部）：无冲突，不涉及nodeadapter/singbox代码

**编译**: ✅ go build ./... 通过
**测试**: ✅ go test ./internal/vowifi/... -count=1（5个包全部通过）

---

## 2026-09-16 同步上游 fd14902 → 621be85
**提交**: 650721c
**提交列表**:
| # | Commit | 描述 |
|---|--------|------|
| 1 | fcb84ee | Update Star History links and image sources |
| 2 | 9592c74 | fix(esim): align RetrieveNotificationsList with OpenEUICC's encoding (#130) |
| 3 | d83e757 | FIX #127 (通知目标地址策略放宽) |
| 4 | 2a2765d | FIX #129 (自动任务蜂窝环境限制移除) |
| 5 | 21e51ad | feat: implement notification integrations, auth service, settings API, and core runtime modules |
| 6 | 621be85 | feat: implement authentication service and settings API |

**操作内容**:

| 阶段 | 文件 | 操作 | 说明 |
|------|------|------|------|
| 1 | README.md | 手动合并 | Star History链接更新为star-history.com API |
| 2 | internal/device/esim_notifications.go | 直接覆盖 | ASN.1编码修复 + undefinedError重试逻辑 |
| 2 | internal/device/esim_notifications_test.go | 直接覆盖 | 测试更新 |
| 2 | internal/device/esim_notifications_fallback_test.go | 新增 | fallback重试测试 |
| 3 | internal/server/settings_api.go | 直接覆盖 | FIX#127私网拦截移除 + 邮件安全 + MIME编码 |
| 3 | internal/server/settings_api_test.go | 直接覆盖 | 测试更新 |
| 4 | internal/server/automatic_tasks.go | 手动合并 | validateAutomaticTaskAvailability移除cellular限制 |
| 4 | internal/server/automatic_tasks_test.go | 直接覆盖 | 测试更新 |
| 4 | internal/store/automatic_tasks.go | 直接覆盖 | SQL查询移除cellular过滤 |
| 4 | internal/store/automatic_tasks_test.go | 直接覆盖 | 测试更新 |
| 4 | web/src/pages/AutomaticTasksPage.tsx | 直接覆盖 | 前端移除cellular环境限制 |
| 5 | internal/auth/service.go | 直接覆盖 | CodeQL注释更新 |
| 5 | internal/server/cellular_data_runtime.go | 直接覆盖 | 日志脱敏 + store导入 |
| 5 | internal/server/email_message.go | 直接覆盖 | 邮件注入安全加固 |
| 5 | internal/server/lark_notification.go | 直接覆盖 | URL构造重构 + CodeQL注释 |
| 5 | internal/server/wecom_notification.go | 直接覆盖 | URL构造重构 + CodeQL注释 |
| 5 | internal/vowifi/ims/rtp_media.go | 手动合并 | payload type边界检查(parsed >= 0 && parsed <= 127) |

**手动合并详情**:
1. README.md: 仅替换Star History链接部分，保留本地部署说明
2. automatic_tasks.go: 仅修改validateAutomaticTaskAvailability函数（移除cellular限制），保留本地Phase检查逻辑(idle/failed)
3. rtp_media.go: 仅在configureRemote函数中添加payload type边界检查，保留本地静音发送器和SIP连接借用逻辑

**与UPDATE.md本地修改的兼容性确认**:
- ✅ 修改#1-#9（HardwareKind、随机IMEI、诊断、SOCKS5 UDP等）：无冲突
- ✅ 修改#10-#37（singbox/nodeadapter相关）：无冲突
- ✅ 修改#38（配额/IMEI/乱码修复）：无冲突
- ✅ 本地automatic_tasks.go的Phase检查逻辑(idle/failed)已保留
- ✅ 本地rtp_media.go的静音发送器和SIP连接借用已保留

**编译**: ✅ go build ./... 通过
**前端**: ✅ npm run build 通过
**测试**:
- ✅ internal/device 通过
- ✅ internal/vowifi (5个包) 通过
- ✅ internal/auth 通过
- ❌ internal/server 编译失败（预先存在：SendDTMF mock缺失，与本次同步无关）
- ❌ internal/store 迁移测试失败（预先存在：call_history表冲突，与本次同步无关）

---

## 2026-09-19 同步上游 621be85 → 84151f4
**提交列表**:
| # | Commit | 描述 |
|---|--------|------|
| 1 | 39b8a53 | fix(ims): hide SIM data download messages from inbox (#131) |
| 2 | 537ecae | fix(ims): reply RP-ACK for SIM data download when UICC handler is not set (#132) |
| 3 | 84151f4 | feat(device): add per-card MBN profile override (#133) |

**操作内容**:

| 阶段 | 文件 | 操作 | 说明 |
|------|------|------|------|
| 1 | internal/device/sms_pdu.go | 直接覆盖 | 隐藏SIM数据下载消息 |
| 1 | internal/device/sms_pdu_test.go | 直接覆盖 | SMS PDU测试 |
| 1 | internal/device/types.go | 直接覆盖 | 类型更新 |
| 1 | internal/vowifi/ims/provider.go | 直接覆盖 | IMS provider更新 |
| 1 | internal/vowifi/ims/sms_runtime.go | 直接覆盖 | SMS运行时更新 |
| 1 | internal/vowifi/ims/sms_runtime_test.go | 直接覆盖 | SMS运行时测试 |
| 2 | internal/vowifi/ims/provider.go | 直接覆盖 | UICC handler未设置时回复RP-ACK |
| 2 | internal/vowifi/ims/sms_runtime.go | 直接覆盖 | SMS运行时更新 |
| 2 | internal/vowifi/ims/sms_runtime_test.go | 直接覆盖 | SMS运行时测试 |
| 3 | internal/device/mbn.go | 直接覆盖 | MBN profile覆盖功能 |
| 3 | internal/device/mbn_test.go | 新增 | MBN profile测试 |
| 3 | internal/device/manager.go | 手动合并 | Options/Manager新增MBNProfileForICCID回调 + 保留本地PCSCIMEIProvider/OnHardwareKindChange/djiIMEICache字段 |
| 3 | cmd/vonet/main.go | 直接覆盖 | MBNProfileForICCID回调实现 |
| 3 | internal/server/settings_api.go | 直接覆盖 | MBN profile设置API |
| 3 | internal/server/settings_api_test.go | 直接覆盖 | 设置API测试 |
| 3 | internal/store/domain_test.go | 直接覆盖 | 域测试 |
| 3 | internal/store/models.go | 直接覆盖 | 模型新增MBNProfile字段 |
| 3 | internal/store/settings.go | 直接覆盖 | 设置存储 |
| 3 | internal/store/migrations.go | 手动合并 | 保留本地case 24(call_history表) + 新增case 25(mbn_profile列) |
| 3 | internal/store/store.go | 手动合并 | schemaVersion 24→25 + duplicateAdditiveColumn nextVersion 24→25 |
| 3 | web/src/components/devices/CardPolicyMBN.tsx | 新增 | MBN profile卡片组件 |
| 3 | web/src/components/devices/CardPolicyPanel.tsx | 直接覆盖 | 卡片策略面板 |
| 3 | web/src/components/devices/EsimCardPolicyInline.tsx | 直接覆盖 | eSIM卡片策略 |
| 3 | web/src/components/devices/deviceActions.ts | 直接覆盖 | 设备操作 |
| 3 | web/src/lib/i18n-en.ts | 直接覆盖 | 英文翻译 |
| 3 | web/src/types.ts | 直接覆盖 | 类型定义 |

**手动合并详情**:
1. **manager.go**: 上游新增MBNProfileForICCID/mbnProfileForICCID字段，本地已有PCSCIMEIProvider/OnHardwareKindChange/djiIMEICache等字段。合并后Options包含所有字段，Manager包含所有字段，NewManager包含所有字段赋值。
2. **migrations.go**: 上游case 24为mbn_profile列，本地case 24为call_history表。保留本地case 24不动，将上游的mbn_profile迁移移至case 25。
3. **store.go**: schemaVersion从24改为25以匹配新的迁移编号；duplicateAdditiveColumn中mbn_profile的nextVersion从24改为25。

**与UPDATE.md本地修改的兼容性确认**:
- ✅ 修改#1（HardwareKind判断）：无冲突，manager.go手动合并保留本地字段
- ✅ 修改#2（OnHardwareKindChange回调 + 状态重置）：无冲突，已保留onHardwareKindChange字段
- ✅ 修改#3（随机IMEI池）：无冲突，已保留pcscIMEIProvider字段
- ✅ 修改#4（诊断XFRM/PCSC）：无冲突
- ✅ 修改#5（开发者模式UI修复）：无冲突
- ✅ 修改#6（WiFi Calling失败日志）：无冲突
- ✅ 修改#7（读卡器AT PORT修复）：无冲突
- ✅ 修改#9（SOCKS5 UDP ASSOCIATE）：无冲突
- ✅ 修改#10-#36（singbox/nodeadapter相关）：无冲突
- ✅ 修改#37（PCSC离线WiFi Calling状态同步）：无冲突
- ✅ 修改#38（设备配额+IMEI池+乱码修复）：无冲突
- ✅ 本地migration 24（call_history表）：已保留
- ✅ 本地duplicateTable处理：已保留

**编译**: ✅ go build ./... 通过

---

## 2026-09-24 同步上游 84151f4 → 484cd23（两次提交）

**提交列表**:

| # | Commit | 描述 | 描述 |
|---|--------|------|------|
| 1 | 4c7e73e | fix(vowifi): support Ultra Mobile VoWiFi and IMS SMS (#137) | Ultra Mobile VoWiFi 和 IMS SMS 支持 |
| 2 | 484cd23 | fix(vowifi): support DITO roaming subscriber identities (#136) | DITO 漫游用户身份支持 |

**操作内容**:

| 阶段 | 文件 | 操作 | 说明 |
|------|------|------|------|
| 1 | internal/vowifi/carrier_compat.go | 直接覆盖 | SubscriberIMSIRewrite + EffectiveSubscriberIMSI |
| 1 | internal/vowifi/carrier_compat_test.go | 直接覆盖 | Ultra Mobile + DITO 测试 |
| 1 | internal/vowifi/ike/eap.go | 直接覆盖 | EAP 更新 |
| 1 | internal/vowifi/ike/eap_test.go | 直接覆盖 | EAP 测试 |
| 1 | internal/device/carrier_db.go | 直接覆盖 | 运营商数据库更新 |
| 1 | internal/device/scan_test.go | 手动合并 | 添加 "None" 测试用例 + 保留本地自定义测试 |
| 2 | internal/vowifi/ims/provider.go | 手动合并 | 见下方详情 |
| 2 | internal/vowifi/ims/provider_test.go | 直接覆盖 | 与上游完全一致 |
| 3 | internal/vowifi/carrier_profiles.json | 手动合并 | DITO + Ultra Mobile 配置更新 |

**provider.go 手动合并详情**:
1. 新增 registerContactICSIRef 常量（mmtel + sms ICSI）
2. buildContact 使用 registerContactICSIRef 替代硬编码
3. deriveIdentities 重排：profile 解析 + EffectiveSubscriberIMSI 调用提前到 IMSI 验证前
4. register() 添加 session.clearAuthentication()（清空旧认证状态）
5. applyRegistrationEvidence 条件化 clearAuthentication：qop=auth 时保留 digest 状态

**carrier_profiles.json 手动合并详情**:

DITO (ipcc-dito-51566):
1. 新增 match_any（漫游赞助商身份：20404, 204047616, 89636626）
2. 新增 identity.subscriber_imsi_rewrite（204047616 → 515661015）
3. ike.proposal 从 "modern" 改为 "legacy-sha1-modp1024"

Ultra Mobile (ipcc-ultramint-mobile-310026):
1. 新增 route: {"mcc": "310", "mnc": "240"}
2. epdg.hostname 从 "ss.epdg.epc.mnc260.mcc310.pub.3gppnetwork.org" 改为 "epdg.epc.mnc240.mcc310.pub.3gppnetwork.org"
3. 新增 ims.register_options.contact_extra_tags（accesstype=wlan1, smsip-msisdnless, smsip-msisdn-less）

**与UPDATE.md本地修改的兼容性确认**:
- ✅ CTEXCEL register_imsi 处理：保留
- ✅ Czech T-Mobile expiry_seconds 7200：保留
- ✅ ExpirySeconds 自定义：保留
- ✅ 修改#1-#9（HardwareKind、随机IMEI、诊断、SOCKS5 UDP等）：无冲突
- ✅ 修改#10-#38（singbox/nodeadapter/设备配额等）：无冲突

**编译**: ✅ go build ./... 通过
**测试**:
- ✅ internal/vowifi 通过
- ✅ internal/vowifi/ike 通过
- ✅ internal/vowifi/ims 通过
- ✅ internal/vowifi/integration 通过
- ✅ internal/vowifi/runtime 通过
- ❌ internal/device TestManagerDiscoversWiFiCallingOnlyReaderWithoutATPort（预先存在，与本次同步无关）

**修复的同步遗漏**:
1. provider.go: registerContactICSIRef 常量 + icsiRef 使用常量
2. provider.go: applyRegistrationEvidence 条件化 clearAuthentication（保留 qop=auth digest）
3. provider.go: deriveIdentities 添加 EffectiveSubscriberIMSI 调用
4. carrier_profiles.json: DITO match_any + identity.subscriber_imsi_rewrite + legacy-sha1-modp1024
5. carrier_profiles.json: Ultra Mobile route + epdg.hostname + contact_extra_tags

---

## 2026-09-28 同步上游 484cd23 → 12ce8f0

**提交**: 12ce8f0 (feat(sms): decode WAP Push MMS notification subjects (#141))

**上游改动**: 新增 `internal/smsdecode` WSP/MMS/SI 解码包；单段 WAP Push 直接显示 MMS Subject 或 SI 文本；多段十六进制在 concat 重组后解码；API 读取旧十六进制短信时预览解码。

**操作内容**:

| 文件 | 操作 | 说明 |
|---|---|---|
| internal/device/sms_pdu.go | 合入 | 8-bit WAP Push 显示解码 |
| internal/device/sms_pdu_test.go | 合入 | MMS、端口寻址、多段测试 |
| internal/device/types.go | 合入 | 新增 `SMSEncodingWAPPush` |
| internal/server/sms_api.go | 手动合并 | 联系人/线程预览解码，保留本地 `blockedSMSDestination(return false)` |
| internal/server/sms_notifications.go | 合入 | 通用通知内容预览解码 |
| internal/smsdecode/*.go | 新增 | WSP、MMS、SI 解码及测试 |
| internal/store/sms_reassembly.go | 合入 | concat 重组后解码，保留原始 `concat_parts` |
| internal/store/sms_reassembly_test.go | 合入 | 多段 WAP Push 解码测试 |

**本地补强**:

1. `internal/server/sms_api.go`：保存 `Extra.raw_user_data`，避免单段 modem WAP Push 解码后原始载荷随自动清理丢失。
2. `internal/server/telegram_bot.go`：Telegram 专用短信通知改用 `formatTelegramInboundSMS` + `smsdecode.Preview`，与通用通知保持一致。
3. 新增 `TestSyncModemSMSRetainsRawUserDataForDecodedWAPPush` 和 `TestFormatTelegramInboundSMSDecodesStoredWAPPush`。

**兼容性确认**:

- ✅ 本地中国号码不拦截策略保留
- ✅ `sms_api_test.go`、`region_test.go` 本地预期保留
- ✅ UPDATE.md 修改 #1-#43 不被本次文件覆盖
- ✅ 多段消息原始载荷仍由 `concat_parts` 保留
- ✅ IMS 原始 RPDU/TPDU 仍由 `Extra.raw_rpdu/raw_tpdu` 保留

**验证**:

- `go test ./... -count=1`：全部通过
- `go build ./...`：通过
- 本次仅更新工作区，未创建提交、未推送 GitHub

---

## 2026-09-29 同步上游 12ce8f0 → 3782ee2（两次提交）

**提交列表**:

| # | Commit | 描述 |
|---|--------|------|
| 1 | 0010752 | feat: add VoWiFi carrier profiles, compatibility matching, and IMS provider support |
| 2 | 3782ee2 | Merge branch 'master' of https://github.com/MengMengCode/VoNet |

**提交拓扑与净变化**:

- `0010752` 的父提交为 `484cd23`，实际新增 VoWiFi carrier profile / IMS 兼容逻辑。
- `3782ee2` 的父提交为 `0010752` 和 `12ce8f0`，其中 WAP Push 文件来自已经同步过的 `12ce8f0`。
- 本次正确同步范围为 `git diff 12ce8f0..3782ee2`，与 `git diff 484cd23..0010752` 的 patch-id 一致。
- 净变化：6 个文件、197 行新增、4 行删除。

**操作内容**:

| 文件 | 操作 | 说明 |
|------|------|------|
| internal/vowifi/carrier_compat.go | 定向合入 | 新增 AT&T ePDG/profile 识别和 ATT REGISTER 默认值；保留本地多 IMSI rewrite、ProvisionedIMSI、EffectiveSubscriberIdentity |
| internal/vowifi/carrier_compat_test.go | 追加测试 | 新增 ATT/Red Pocket 断言和 TestCarrierSwapSeparation；保留 DITO、Ultra Mobile、ProvisionedIMSI 测试 |
| internal/vowifi/carrier_ipcc.go | 定向合入 | 新增 RegistrationPolicy=ATT、WifiAccessInfo PANINode 导入和 ATT profile 设置 |
| internal/vowifi/carrier_profiles.json | 两行追加 | Red Pocket 增加 identity_profile/register_profile=att；保留 DITO、Ultra Mobile 和其他本地 profile |
| internal/vowifi/ims/provider.go | 定向合入 | 新增 ATT Cellular-Network-Info 和 SimAdmin VoWiFi User-Agent fallback；保留身份、SMS ICSI、认证状态和注册刷新修复 |
| internal/vowifi/ims/provider_test.go | 追加测试 | 新增 ATT/Red Pocket REGISTER 测试；保留 DITO、ProvisionedIMSI、SMS 和认证测试 |

**WAP Push 内容确认**:

- `3782ee2` 相对 `0010752` 显示的 `internal/smsdecode`、SMS PDU、SMS API、SMS reassembly 文件属于 `12ce8f0`，本次不再重复覆盖。
- 本地 `UPDATE.md` #44 的 raw_user_data 保留和 Telegram WAP Push 预览修复继续保留。
- 本次没有遗漏 `0010752` 的任何文件或 hunk；当前工作树 patch-id 与上游净 patch 完全一致。

**本地功能兼容性确认**:

- ✅ DITO 多条 IMSI rewrite 和 ProvisionedIMSI 身份优先级保留
- ✅ Ultra Mobile route/ePDG/contact_extra_tags 保留
- ✅ registerContactICSIRef、qop=auth 认证状态保留、clearAuthentication/applyRegistrationEvidence 保留
- ✅ UPDATE.md #45-#47 的 IMEI 租约、IMEI 池、代理路由、分页功能未被修改
- ✅ 未修改前端、store、server、pcsc、cmd/vonet 等本地功能文件

**验证**:

- ✅ `go test ./internal/vowifi ./internal/vowifi/ims ./internal/vowifi/ike` 通过
- ✅ `go test ./...` 全部通过
- ✅ `git diff --check` 通过

本次提交仅包含上述 6 个同步文件和本同步记录，不包含其他本地修改。

---

## 2026-10-01 同步上游 3782ee2 → 875ec3d（七次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 8bb8710 | feat: add inbound SMS and automatic task notification dispatchers | 入站短信与自动任务通知分发 |
| 2 | e8a598c | feat: add VoWiFi carrier profiles, IMS provider, security, and compatibility layers with tests | VoWiFi 运营商配置、IMS Provider、安全与兼容层 |
| 3 | 1c66702 | feat(device): support ml307 device (#144) | ML307 设备类型、发现、配置与资源 |
| 4 | ecaeda5 | feat: add DeviceConfigTab component for device configuration management | DeviceConfigTab 设备配置管理 |
| 5 | de2659a | fix(sms): allow deleting history after device removal (#145) | 删除设备后允许清理短信历史 |
| 6 | b02286a | fix(sms): bound notification retries so one failure cannot stall a channel (#146) | 短信通知有界指数退避与最大重试次数 |
| 7 | 875ec3d | fix(vowifi): use the dedicated IMS APN for the ePDG tunnel (#148) | ePDG/IKE 使用独立 IMS APN |

**同步范围**:

- 上游净变化为 41 个文件，其中 36 个修改、5 个新增；当前工作树已完整包含全部文件和改动。
- 同步基线采用 `git diff 3782ee2..875ec3d`，没有重复套用已经合入的 WAP Push 或更早 VoWiFi 改动。
- 涉及 README、CLI 启动、设备识别/快照、短信与通知、VoWiFi/IMS、设置存储、设备配置前端和 ML307 资源。

**逐文件/逐模块合并情况**:

| 文件或模块 | 操作 | 说明 |
|---|---|---|
| README.md | 合入 | 更新上游能力与配置说明 |
| cmd/vonet/main.go | 手动合并 | 接入新设置/服务初始化，保留本地构建与启动逻辑 |
| internal/device/esim.go、esim_test.go | 合入 | eSIM 状态与测试更新 |
| internal/device/manager_test.go | 合入 | 自动发现、ML307 与设备管理测试 |
| internal/device/mbn.go、mbn_test.go | 手动合并 | MBN 匹配更新，并保留本地候选 VendorID TrimSpace 语义 |
| internal/device/snapshot.go | 合入 | 设备快照扩展，支持新设备状态和配置展示 |
| internal/modem/discovery.go、discovery_test.go、family.go | 合入 | 新增 modem family 分类与 ML307 发现支持 |
| internal/server/automatic_task_notifications.go | 合入 | 自动任务通知分发接入 |
| internal/server/device_api.go、device_discovery_api_test.go | 手动合并 | ML307 设备 API 与发现测试，保留 PCSC 物理匹配、IMEI 租约和设备类型限制 |
| internal/server/sms_api.go、sms_api_test.go | 手动合并 | 删除设备后的短信历史处理，保留 WAP Push raw_user_data、预览解码及本地短信策略 |
| internal/server/sms_notifications.go、sms_notifications_test.go、sms_notifications_retry_test.go | 合入/补测试 | 入站短信通知、自动任务通知、有界退避重试与游标推进 |
| internal/server/vowifi_settings.go、vowifi_settings_test.go | 合入 | VoWiFi 设置 API 增加 IMS APN 与回归测试 |
| internal/store/devices.go、domain_test.go | 手动合并 | 设备数据字段与短信历史删除测试，保留 IMEI 池、租约和代理绑定模型 |
| internal/vowifi/carrier_compat.go、carrier_compat_test.go | 手动合并 | 新增运营商兼容匹配，保留 DITO、Provisioned IMSI、ATT、Red Pocket 等本地身份逻辑 |
| internal/vowifi/carrier_profiles.json | 手动合并 | 合入新增运营商 profile，并保留本地 profile 定制 |
| internal/vowifi/ec20_adapter.go、ec20_adapter_test.go | 合入 | EC20/ML307 相关适配与测试 |
| internal/vowifi/ims/provider.go、provider_test.go | 手动合并 | IMS Provider、运营商注册、安全兼容与测试，保留本地 REGISTER/SMS/认证修复 |
| internal/vowifi/ims/security.go、security_provider_test.go | 合入 | IMS 安全能力与 Provider 安全测试 |
| internal/vowifisettings/settings.go、settings_test.go | 合入 | 持久化独立 IMS APN，空值恢复默认 `ims` |
| web/public/ml307.png | 新增 | ML307 设备图标 |
| web/src/components/devices/CellularIMSConfigCard.tsx | 合入 | ML307 隐藏 Cellular IMS 专有配置，并显示行内加载错误 |
| web/src/components/devices/DeviceAddDialog.tsx | 合入 | ML307 类型、图标与添加设备配置适配 |
| web/src/components/devices/DeviceConfigTab.tsx | 合入 | ML307 配置展示，AT 模式标签统一为 `AT` |
| web/src/components/settings/VoWiFiMTUCard.tsx | 本地补强 | 增加 VoWiFi 连接设置中的 IMS APN 编辑与保存 |
| web/src/lib/deviceTypes.ts、web/src/types.ts | 合入 | ML307 类型、设备属性和配置类型更新 |
| web/src/lib/i18n-en.ts | 合入并补翻译 | 增加 ML307、AT、Cellular IMS 错误及 IMS APN 英文文案 |

**修复的同步遗漏**:

1. 补入 `internal/modem/family.go`，统一 ML307 modem family 与 Linux USB identity 归一化。
2. 自动创建的 ML307 设备名称同步进行类型名称归一化，避免显示原始 USB 描述。
3. 保留 `internal/device/mbn.go` 中 `strings.TrimSpace(candidate.VendorID)`，避免本地候选匹配回归。
4. 在 `web/src/components/settings/VoWiFiMTUCard.tsx` 补入 IMS APN 编辑能力，使 `/api/settings/vowifi` 新设置可在前端配置。
5. 补齐 `web/src/lib/i18n-en.ts` 中本次前端新增文案的英文翻译。
6. 补入 `internal/vowifisettings/settings_test.go` 空 IMS APN 重置默认值测试。
7. 补入 ML307 Linux USB 识别、自动设备创建、短信通知重试和 VoWiFi 设置回归测试。

**本地功能保留确认**:

- ✅ PCSC IMEI 租约、固定/随机 IMEI、IMEI 池及池耗尽降级行为保留。
- ✅ 自动发现自动补建设备、缺失租约自动修复及物理硬件校验保留。
- ✅ DJI/Baiwang 身份匹配、DITO/Provisioned IMSI/ATT/Red Pocket 定制保留。
- ✅ WAP Push `raw_user_data`、MMS/SI 预览解码、多段重组和 Telegram 通知解码保留。
- ✅ 读卡器类型不可变、双向类型限制和 PCSC 物理匹配规则保留。
- ✅ ICCID 多代理顺序路由、每代理三次尝试、断线续跑和路由重启行为保留。
- ✅ Node adapter/sing-box 集成、IMEI 池和上游代理分页保留。
- ✅ 中国本地短信非阻塞策略保留。
- ✅ `UPDATE.md` 中本地修复和新增功能记录未被本次同步修改。

**前端可见变化**:

- 设备类型与添加/编辑页面新增 ML307，使用 ML307 图标。
- ML307 隐藏不适用的 Cellular IMS 配置；配置加载失败改为页面内错误提示。
- 串口模式标签统一显示为 `AT`。
- VoWiFi 连接设置新增 IMS APN 输入项，默认值为 `ims`，可按运营商需要覆盖。

**验证**:

- ✅ `go test ./...` 全部通过。
- ✅ `npm test` 全部通过。
- ✅ `npm run build` 通过，仅有原有 large chunk 提示。
- ✅ `git diff --check` 通过。
- ✅ 上游 41 个文件及净 diff 已逐文件复核，无未同步文件或遗漏 hunk。

本次同步更新已写入此记录，随后作为一次提交推送至 GitHub `master`。

---

## E911 与 IKE 更新同步（ba17d50 → 2d023a7）

**上游提交**:

| # | Commit | 描述 |
|---|--------|------|
| 1 | 0739ac2 | Implement E911 address provisioning with carrier detection and verification |
| 2 | a3bfd01 | feat(e911): Implement E911 registration and entitlement handling |
| 3 | 2d023a7 | Delete docs/E911.zh-CN.md |

**同步范围**:

- 目标范围为 `git diff ba17d50..2d023a7`，净变化 30 个代码/前端文件，新增 2626 行、删除 451 行。
- `docs/E911.zh-CN.md` 按第一次和第二次提交保留。第三次上游提交删除了该文档，但本地保留其中的实卡验证限制、协议依据和 API 说明。
- 旧自托管 `/websheets/` 地址流程及其 iframe 组件已移除；历史 `e911_address:*` 数据不迁移为成功状态，新流程仅缓存运营商确认的 `e911_confirmed:*` 元数据。

**冲突文件处理**:

| 文件 | 处理 |
|---|---|
| internal/server/device_api.go | 合入 E911 路由和设备摘要字段；保留 PCSC IMEI、设备硬件匹配、DJI 离线状态及电话号码回退 |
| web/src/components/devices/DeviceOverviewTab.tsx | 保留本地小区锁定按钮和 `statusKnown` 状态，不采用上游删除 VoWiFi 状态下按钮的改动 |
| web/src/lib/i18n-en.ts | 追加 E911 文案；保留 IMS APN、代理绑定、PCSC、小区锁定等本地文案 |
| web/src/components/devices/CarrierWebsheetDialog.tsx | 删除旧组件，并清理 `websheet-frame-shell` 样式和旧错误文案 |

**额外兼容修正**:

- E911 carrier detection 同时使用 EF_AD MNC 和 IMSI 前六位候选 PLMN，覆盖 MNC 长度为 2 位时的 T-Mobile/RedPocket 识别。
- TS.43 token 查询使用 `EffectiveSubscriberIdentity()`，与 EAP-AKA 的 Provisioned IMSI/Carrier Profile 改写保持一致。
- 新增 `ReadIdentityWithoutRotation`；E911/entitlement 的 `WithSIM` 读取不消费 PCSC 随机 IMEI 轮换，普通 VoWiFi 尝试/重试仍保持原轮换策略。
- `WithSIM` 回调携带可取消 context；runtime manager 跟踪 provisioning 状态，阻塞并发生命周期操作，并在 `Reset/Remove/Close` 时取消运营商请求。
- E911 弹窗在未实卡验证提示、成功关闭和启动 loading 状态上与实际流程保持一致。

**本地功能保留确认**:

- ✅ DITO/Provisioned IMSI、Carrier Profile、脱敏身份诊断和 IMS 身份改写保留。
- ✅ PCSC 固定/随机 IMEI、IMEI 池、AUTH_FAILED 轮换策略和设备双 key 映射保留。
- ✅ 代理路由重置、粘性路由、MCC 回退、自动重试错峰和 `Remove` 生命周期修复保留。
- ✅ PCSC/DJI/Baiwang 设备匹配、USB 序列号 ID、IMEI 租约和设备离线状态保留。
- ✅ 小区锁定未知状态显示、Native410 feature boundary 和本地英文文案保留。

**验证**:

- ✅ `go test ./...` 全部通过。
- ✅ `npm test` 25 项全部通过。
- ✅ `npm run build` 通过，仅有原有大 chunk 警告。
- ✅ `git diff --check` 通过。
- ⚠️ `go vet ./internal/vowifi/e911 ./internal/vowifi/ike ./internal/vowifi ./internal/vowifi/runtime ./internal/server` 仅报告既有 `internal/server/cell_lock_api_test.go` 未命名结构体字面量告警。

本次同步通过 `conflict-analysis` 分支提交 PR，全部检查通过后合并到 GitHub `master`。

---

## 2026-10-03 同步上游 875ec3d → 1fa3bf6（五次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | a1273f0 | FIX #143 | 交互式 USSI 会话、multipart USSI/SMS 分类和运行时取消能力 |
| 2 | 2bb43de | Merge branch 'master' of https://github.com/MengMengCode/VoNet | 合并 USSI 变更与既有 IMS APN、短信通知重试主线，无额外净变化 |
| 3 | 3cbb6c1 | FIX #149 | eSIM ISD-P AID 寻址、ICCID 失败 fallback 和切换失败状态恢复 |
| 4 | 5bab56f | feat: add registration-only cellular task (#150) | 仅注册蜂窝网络自动任务、数据关闭和卡策略恢复 |
| 5 | 1fa3bf6 | feat(sms): add JSON export and offline HTML reader (#151) | 短信 JSON 导出、离线 HTML 阅读器和前端导出入口 |

**同步范围**:

- 上游 `875ec3d..1fa3bf6` 净变化为 40 个文件；五个提交曾涉及的并集为 48 个文件，其余 8 个文件在合并或后续提交中没有最终净变化。
- 当前工作树相对 GitHub `master`（`ee993ef`）共修改 42 个文件，其中 40 个属于本次上游净变化，另外 2 个是兼容本地 PCSC 随机 IMEI 历史的 `internal/store/models.go` 和 `internal/store/sms.go`。
- 同步采用 `git cherry-pick --no-commit` 按提交顺序合入，并针对本地 schema、WAP Push、multipart SMS/USSI 和 PCSC IMEI 历史进行兼容处理。

**逐模块合并情况**:

| 文件或模块 | 操作 | 说明 |
|---|---|---|
| internal/device/esim.go、esim_disable.go、esim_test.go | 合入 | 增加 AID Enable/Disable 请求、ICCID 到 AID 解析、SGP.22 错误映射和 AID fallback |
| internal/server/esim_api.go、device_features_api_test.go | 合入 | eSIM 切换失败后恢复飞行模式或 VoWiFi，并补回归测试 |
| internal/server/device_api.go、device_features_api.go、ussd_sessions.go | 定向合入 | 增加 USSI 网络会话 ID、继续输入和网络取消；保留 PCSC/IMEI、物理匹配及设备类型限制 |
| internal/vowifi/ims/ussi_runtime.go、ussi_runtime_test.go | 新增 | 实现 INVITE/ACK/BYE 交互式 USSI 会话和完整协议测试 |
| internal/vowifi/ims/sms_runtime.go、sms_runtime_test.go | 手动合并 | 接入交互式 USSI；增加 multipart 分类，SMS 同时存在时保持 SMS 路径 |
| internal/vowifi/ims/call_runtime.go、provider.go | 定向合入 | 增加 USSI 会话清理和 CANCEL/BYE 保护；保留 DTMF、静音保活、身份与注册修复 |
| internal/vowifi/orchestrator.go、runtime/manager.go、types.go | 手动合并 | 增加 USSI 取消链路；保留多代理切换、粘性路由、错峰重试、AUTH_FAILED 和 SIM 稳定期逻辑 |
| internal/server/automatic_tasks.go、automatic_task_notifications.go | 合入 | 增加 `cellular_attach` 任务、数据关闭、自动选网、注册等待和通知文案 |
| internal/server/automatic_registration_test.go、automatic_registration_switch_test.go | 新增 | 覆盖注册任务、Profile 切换、数据意图恢复和读卡器限制 |
| internal/store/migrations.go、store.go、automatic_registration_migration_test.go | 手动合并 | 上游 migration 25 适配为本地 migration 30，保留本地 25/26/28/29 和任务历史数据 |
| internal/server/sms_export.go、sms_export_html.go、sms_export.html、sms_export.css、sms_export.js | 新增/补强 | JSON/HTML 流式导出、CSP、时区和离线搜索；HTML 对旧 WAP Push 使用 `smsdecode.Preview()` |
| internal/store/sms_export.go、sms_export_test.go、internal/server/sms_export_test.go、sms_export_timezone_test.go | 新增/补测试 | 导出查询、JSON schema、HTML 安全、时区、失败响应和旧 WAP Push 回归测试 |
| web/src/components/sms/SmsExportModal.tsx、smsExport.ts、web/test/smsExport.test.mjs | 新增 | 短信导出弹窗、日期/时区/格式选择和前端回归测试 |
| web/src/pages/SmsPage.tsx、web/src/lib/i18n-en.ts、web/src/pages/AutomaticTasksPage.tsx | 合入 | 增加短信导出入口、注册任务选项与英文翻译，保留读卡器任务限制和现有短信操作 |
| internal/server/sms_api.go、internal/store/models.go、internal/store/sms.go | 手动合并 | 增加导出路由和 `MatchDeviceOrIMEI`，兼容 PCSC 随机 IMEI 轮换前后的短信历史 |

**兼容处理和同步复核**:

1. 本地数据库已有 migration 25/26/28/29，因此将上游自动任务 migration 调整为 case 30，`schemaVersion` 同步升级为 30。
2. migration 30 保留自动任务和运行历史、外键及 AUTOINCREMENT 水位；测试改为 `TestMigration30PreservesTasksAndRuns`，旧库基线使用 v29。
3. 短信 JSON 同时保留原始 `body` 和新增 `display_body`；HTML 使用解码预览，旧 WAP Push 不再显示原始 WSP/hex。
4. PCSC 短信历史查询同时匹配稳定设备 ID 与当前 IMEI，避免随机 IMEI 轮换后历史会话消失。
5. multipart USSI 根据子类型分类；消息同时包含 SMS 与 USSI 时 SMS 优先，避免普通短信被交互式 USSI 处理器消费。
6. 五个提交涉及的 48 个文件和上游净 patch 已逐文件复核；30 个文件可直接反向应用净补丁，其余 10 个因本地兼容差异人工核对，无遗漏 hunk。

**本地功能保留确认**:

- ✅ `UPDATE.md` #1-#48 的本地新增功能与修复未被覆盖。
- ✅ PCSC 固定/随机 IMEI 租约、IMEI 池、自动设备租约修复和池耗尽降级保留。
- ✅ PCSC USB 序列号匹配、设备类型不可变、重复设备检测和删除竞态修复保留。
- ✅ ICCID 多代理顺序、失败三次切换、断联粘性重试、路由计划变化重置和 MCC 回退保留。
- ✅ DITO/Provisioned IMSI、IMS APN、AUTH_FAILED、SIM 稳定期、错峰启动和 VoWiFi 状态恢复保留。
- ✅ RTP 静音保活、DTMF、通话双方音频和 CANCEL/BYE 修复保留。
- ✅ WAP Push `raw_user_data`、MMS/SI 解码、Telegram 预览、多段重组和短信通知退避保留。
- ✅ Node adapter/sing-box、HTTP/SOCKS5 转换、IMEI 池与上游代理分页功能未被修改。
- ✅ 中国本地短信非阻塞策略保留。

**验证**:

- ✅ `go test ./...` 全部通过。
- ✅ `npm test` 21 项全部通过。
- ✅ `git diff --check` 通过。
- ✅ GitHub `master` 最新提交与同步前本地 `HEAD` 均为 `ee993ef`，未发现本次更新覆盖已有本地功能。

本次同步更新已写入此记录，随后作为一次提交推送至 GitHub `master`。

---

## 2026-10-04 同步上游 1fa3bf6 → e129aec（两次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 3fa53a0 | fix(esim): use host reset for PC/SC profile switches (#152) | PC/SC eSIM Profile 切换改为 host reset，并补充真实 ICCID 验证 |
| 2 | e129aec | feat(device): support LTE cell locking (#153) | LTE 服务小区/邻区查询，以及 EARFCN/PCI 单小区锁定与解锁 |

**同步范围**:

- 上游两次提交累计涉及 39 个路径：PC/SC eSIM 2 个文件，LTE 小区锁定 37 个文件。
- 当前同步内容直接覆盖其中 25 个功能路径，包括设备层小区查询/锁定、ML307/Quectel AT 解析、API、前端入口和测试。
- 另外 14 个上游文件只修改 Modal 宽度或弹窗布局；按照此前本地 UI 方案有意保留原尺寸，没有套用上游 680px 样式。
- 上游 `internal/modem/family.go -> internal/modem/imei.go` 重命名已同步，旧文件未残留。

**逐模块合并情况**:

| 文件或模块 | 操作 | 说明 |
|---|---|---|
| internal/device/esim.go、esim_pcsc_test.go | 合入并适配测试 | PC/SC 使用 `refresh=false` 和 `CloseWithReset()` 应用 Profile，切换后校验真实 ICCID；保留 ISD-P AID fallback。测试 fake Reader 增加本地发现逻辑要求的 `USBSerial` |
| internal/device/cell_lock.go、cell_lock_dialect.go | 合入并兼容 | 新增小区查询、锁定状态读取、EARFCN/PCI 校验、写后读回验证；保留 PCSC/Baiwang 硬件能力边界 |
| internal/device/at_parse.go | 新增 | 接收从 `snapshot.go` 拆出的通用 AT/CSV/数值解析 helper |
| internal/device/snapshot.go、types.go | 上游重构 | 删除已拆分的 QENG/ML307 解析 helper，保留 Native QMI、SIM 热插拔恢复、SPN/MNC/GID、ML307 和 IMEI 保留逻辑 |
| internal/device/vendor_ml307.go、vendor_quectel.go | 新增 | 分别实现 ML307 与 Quectel 服务小区、邻区、频段和信号解析 |
| internal/device/cell_lock_test.go | 合入并补测试 | 覆盖 ML307、Quectel、邻居小区、参数校验、读回失败和不支持硬件；增加本地硬件能力路由测试 |
| internal/modem/discovery.go、imei.go | 合入 | 导出 `IsQuectelUSBModem`、同步 `IsML307` 和 IMEI helper；保留 ReadIMEI、Baiwang/DJI、USB identity 和候选 ID 逻辑 |
| internal/server/cell_lock_api.go、cell_lock_api_test.go | 合入并补测试 | 新增 `/cells`、`/cell-lock` GET/PUT/DELETE API 和错误映射；修正测试夹具的 `Cells()` 错误返回，使 501 路径真实生效 |
| internal/server/device_api.go、device_summary_test.go | 手动合并 | 注册小区 API 路由并输出 `supports_cell_lock`；保留 PCSC IMEI 租约、设备类型不可变、物理匹配和 USB 身份逻辑 |
| internal/server/device_features_api_test.go、pcsc_device_api_test.go | 本地补强 | 补充 cells/cell-lock 未支持路径及 PCSC/410 不暴露小区能力的回归测试 |
| web/src/components/devices/CellLockButton.tsx、CellLockDialog.tsx、useDeviceCellLock.ts | 新增 | 小区锁定按钮、查询/锁定弹窗、状态与并发读取 hook；额外修正加载状态和未知状态下的解锁保护 |
| web/src/components/devices/DeviceOverviewTab.tsx、OverviewNetworkCard.tsx | 合入 | 在设备概览和网络卡片中加入小区锁定入口，并传递锁定状态 |
| web/src/pages/DevicesPage.tsx、types.ts、i18n-en.ts | 合入 | 增加设备详情 key、小区类型和 `supportsCellLock`，补齐英文文案；保留本地 PCSC IMEI、代理和 VoWiFi 类型 |

**有意未同步的上游样式变化**:

- `CardPolicyAPN.tsx`、`CarrierWebsheetDialog.tsx`、`DeleteProfileModal.tsx`、`DeviceAddDialog.tsx`、`EsimNotificationsModal.tsx`、`OperatorSelectionDialog.tsx`、`CountryRulesDialog.tsx`、`DeviceBindingsDialog.tsx`、`UpstreamDialog.tsx`、`NewSmsModal.tsx`、`SmsExportModal.tsx`、`Modal.tsx`、`AutomaticTasksPage.tsx`、`ExportProxyPage.tsx` 仅涉及弹窗尺寸统一。
- 保留本地 `width`、`className`、`bodyClassName` 和各弹窗原尺寸，避免覆盖代理绑定、IMEI 选择和短信导出等本地 UI。

**本地功能保留确认**:

- ✅ PCSC 固定/随机 IMEI、独立租约表、IMEI 池、池耗尽降级和自动租约修复保留。
- ✅ 读卡器类型双向不可变、`immutable_reader_device_type`、物理硬件校验和 USB 序列号匹配保留。
- ✅ 自动添加设备、缺失租约自愈、重复设备检测和设备配置页 PCSC IMEI 选择保留。
- ✅ 多代理顺序路由、三次失败切换、粘性重试、路由计划重置和 MCC 回退保留。
- ✅ WAP Push/MMS/SI 预览、`raw_user_data`、短信通知退避和 JSON/HTML 导出保留。
- ✅ DITO/Provisioned IMSI、Carrier Profile、独立 IMS APN、VoWiFi 安全和 USSI 逻辑保留。
- ✅ IMEI 预设池扩充、IMEI 池与上游代理每页 10 条分页保留。
- ✅ `snapshot.go` 删除的 158 行均是拆分到 `at_parse.go`、`types.go`、`vendor_ml307.go` 和 `vendor_quectel.go` 的上游重构，不是功能丢失。
- ✅ `esim.go` 仅注释和 `ErrESIMCommandError` 文案采用上游通用描述，AID fallback 与本地兼容行为未改变。

**验证**:

- ✅ `go test ./...` 全部通过。
- ✅ `npm test` 21 项全部通过。
- ✅ `npm run build` 通过。
- ✅ `git diff --check` 通过。
- ✅ 与 GitHub `master` 最新提交逐文件比较，未发现本地新增功能或 bug 修复被功能性覆盖。

本次同步更新已写入此记录，随后作为一次提交推送至 GitHub `master`。


---

## 2026-10-11 同步上游 e129aec → b16a69c（14 次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 3046d5b | fix(sms): 统一使用原生顶部导出提示 (#154) | 短信导出结果改为原生顶部提示 |
| 2 | 8b26932 | 新增 MeoW 通知并支持清空 Telegram 和邮件配置 (#155) | MeoW 通知渠道和通知凭据清空 |
| 3 | 0dec0cb | 修复 MeoW 推送正文重复标题 (#157) | MeoW 正文去重和调用入口测试 |
| 4 | 3f0b3ae | #158 | PCSC USIM/ISIM 应用发现、鉴权偏好和 IMSI 绑定回退 |
| 5 | cf3c861 | Merge branch 'master' of https://github.com/MengMengCode/VoNet | 合并前面短信、MeoW、LTE 小区锁定分支 |
| 6 | e89a2d4 | fix(modem): prevent serial descriptor inheritance (#160) | 串口增加 close-on-exec |
| 7 | f594fac | fix(esim): finalize profile switches after client disconnect (#159) | eSIM 切换结果在客户端断开后继续完成 |
| 8 | f49e3f6 | fix(device): preserve RF-off intent during SIM recovery (#161) | SIM 恢复保持 RF-off、记录恢复失败和结果 |
| 9 | 41b6ac6 | 修复 SIM 应用访问兼容与通道清理，补充 eSIM 失败重试和重启提示 (#163) | QMI 通道清理、EC20 ISIM/CSIM fallback、错误链 |
| 10 | 048b929 | fix(call): detect incoming calls when cellular data is disabled (#164) | 蜂窝数据关闭时仍检测普通 Modem 来电 |
| 11 | 6c47024 | 撤回旧修复并为 eSTK 管理应用异常添加一次 SIM 恢复 (#167) | 撤回中间探测改动，增加明确 eSTK 异常的一次 SIM reset |
| 12 | df68879 | feat(device): support ml307x device (#168) | ML307X USB identity 和 AT interface |
| 13 | ab3ca2a | feat: add automatic tasks and background scheduler for routine device operations | 自动任务完成后切回原 Profile 并恢复环境 |
| 14 | b16a69c | feat: add security settings, logging retention limits, and login lockout management APIs and UI components | 日志最低级别保留、清理和前端设置 |

**同步范围**:

- 目标范围为 `git diff e129aec..b16a69c`，最终净变化 68 个路径；14 个提交涉及的路径并集为 71 个。
- 3 个提交涉及但最终净变化为零：`internal/server/device_api.go`、`internal/device/esim_qmi_probe_test.go`、`internal/server/esim_error_api_test.go`；未重新带入中间提交内容。
- 57 个净文件可直接反向检查通过，11 个文件因本地兼容逻辑人工合并；工作区最终变更集合与 68 个净路径完全一致。

**人工合并与兼容处理**:

| 文件或模块 | 处理 |
|---|---|
| internal/device/manager.go | 保留 PCSC IMEI provider、硬件类型切换、DJI/Baiwang IMEI 缓存，同时合入 eSIM recovery 结果、CLCC 容错、RF-off 恢复和 ML307X 支持 |
| internal/pcsc/service.go | 保留 ProvisionedIMSI、DITO 身份和 USB selector 兼容，合入 USIM/ISIM AID 发现、`AuthenticateWithPreference()` 和 `isim_strict` |
| internal/vowifi/pcsc_adapter.go | 保留 IMEI 轮换、AUTH_FAILED 粘性策略、DITO 路由和 ProvisionedIMSI，合入 Preferred AKA 与 IMSI 绑定回退 |
| internal/vowifi/pcsc_adapter_test.go | 合并本地 DITO 测试与上游 Preferred AKA/ISIM 测试 |
| internal/server/automatic_tasks.go | 保留已有 VoWiFi 在途状态不重复重连逻辑，增加任务完成后切回原 Profile |
| internal/store/migrations.go、store.go、automatic_registration_migration_test.go | 上游 `revert_profile` migration 重编号为本地 migration 31，保留本地 migration 25/26/28/29/30 和历史数据 |
| internal/store/models.go、settings.go | 保留 PCSC IMEI、`MatchDeviceOrIMEI` 等本地字段，增加 `RevertProfile`、`ClearSensitiveFields` |
| web/src/lib/i18n-en.ts | 合并 MeoW、自动任务和日志级别文案，保留本地代理、IMEI、VoWiFi 翻译 |

**功能保留确认**:

- PCSC 固定/随机 IMEI、IMEI 池、租约修复和 USB 身份匹配保留。
- 设备类型不可变、PCSC 物理匹配、代理多路由、粘性重试和 MCC 回退保留。
- WAP Push/MMS/SI 解码、短信通知退避、短信导出和 `MatchDeviceOrIMEI` 保留。
- DITO/Provisioned IMSI、Carrier Profile、IMS APN、USSI 和 VoWiFi 安全逻辑保留。
- `snapshot.go` 的 helper 拆分属于上游重构，Native QMI、SIM 热插拔、SPN/MNC/GID 和 IMEI 保留逻辑未丢失。

**验证**:

- `go test ./...` 全部通过。
- `npm test` 23 项全部通过。
- `npm run build` 通过，仅有原有大 chunk 警告。
- `git diff --check` 通过。

本次同步更新已写入此记录，随后与本地 bug 修复一并提交至 GitHub `master`。


---

## 2026-10-13 同步上游 b16a69c → ba17d50（3 次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 6ba57a3 | feat: add per-device notification filtering (#169) | 所有通知渠道支持按设备过滤，增加通知范围 API、后端路由测试和前端选择控件 |
| 2 | 5d4b1f9 | fix(modem): prevent AT command response desynchronization (#170) | 重构 AT Session、串口/WWAN transport 和超时迟到响应处理 |
| 3 | ba17d50 | FIX #171 | 短信通知保留历史 ICCID/本机号码，扩展 Webhook、WeCom、Lark 和 Telegram 展示字段 |

**同步范围**:

- 目标范围为 `git diff b16a69c..ba17d50`，最终净变化 35 个文件，新增 1835 行、删除 927 行。
- 三次提交涉及的路径并集同样为 35 个，没有提交后被撤回或最终净变化遗漏的文件。
- 当前工作区共变更 36 个路径，额外的 `internal/server/telegram_bot_test.go` 是本地 WAP Push 回归测试适配。
- 33 个文件可机械反向验证；`internal/device/manager.go`、`internal/server/telegram_bot.go` 因保留本地兼容逻辑人工合并，但上游 hunk 已逐项确认存在。

**逐模块合并情况**:

| 文件或模块 | 处理 |
|---|---|
| internal/server/automatic_task_notifications.go、call_notifications.go、sms_notifications.go | 合入按设备过滤；保留 MeoW 标题处理、短信有界重试和 PCSC 跳过 `AT+CLCC` |
| internal/server/lark_notification.go、meow_notification.go、wecom_notification.go | 增加设备范围判断和 `local_phone`、`iccid`、`recipient` 模板变量 |
| internal/server/settings_api.go | 支持 `device_ids=null/[]/ID 列表`，保留 MeoW、`clear_secrets` 和敏感配置处理 |
| internal/server/telegram_bot.go、telegram_bot_test.go | 合入 Telegram 设备过滤和统一 `TelegramText()`；保留 `smsdecode.Preview()` WAP Push 解码测试 |
| internal/server/sms_notifications_test.go | 合并本地短信测试与上游历史收信卡、重试保持、Webhook/WeCom/Lark 模板测试 |
| internal/server/notification_device_scope_test.go、notification_routing_test.go | 新增通知范围 API、渠道路由和手动测试忽略设备范围测试 |
| internal/modem/session.go、session_test.go | 合入 pending transaction、迟到 final 归属、URC 分流、超时恢复和并发 Session 测试 |
| internal/modem/serial_linux.go、wwan_at_linux.go 及测试 | 合入 fd 生命周期、poll 超时、Drain 输入保留和关闭并发保护 |
| internal/device/manager.go、cellular_ims.go、mbn.go、sms.go、snapshot.go、cell_lock.go | 合入中断命令不重开连接、Native QMI DMS IMEI、迟到响应处理；保留 PCSC IMEI、硬件切换、RF-off 和硬件能力边界 |
| web/src/components/settings、web/src/pages/SettingsPage.tsx、web/src/types.ts | 增加设备范围控件和 `deviceIds` 类型，保留本地 MeoW、清空配置、代理和 IMEI UI |

**本地功能保留确认**:

- ✅ PCSC 固定/随机 IMEI、IMEI 池、租约、自动修复和 USB 身份匹配保留。
- ✅ 读卡器跳过 `AT+CLCC` 的本地修复保留。
- ✅ 代理多绑定、循环切换、粘性路由、MCC 回退和 `MatchDeviceOrIMEI` 保留。
- ✅ WAP Push/MMS/SI 解码、`raw_user_data`、短信退避和 Telegram 预览保留。
- ✅ DITO/Provisioned IMSI、Carrier Profile、IMS APN、USSI 和 VoWiFi 安全逻辑保留。
- ✅ `snapshot.go` 的 Native QMI 改动保留 `previousSnapshot.IMEI`，未删除历史 IMEI 保持逻辑。
- ✅ `manager.go` 保留 PCSC IMEI provider、硬件类型变化回调、DJI/Baiwang IMEI 缓存和物理匹配逻辑。

**实现替换说明**:

- SMS 发送超时后不再手动关闭连接，改由 AT Session 保留迟到 `+CMGS/OK`。
- Cell lock 缺少 final 时不再手动关闭 client，改由 Session 恢复机制处理。
- Native QMI IMEI 改为 DMS 优先并跳过 Native QMI 的 AT+CGSN。
- Telegram 短信格式统一由 `TelegramText()` 输出，但正文仍经 `smsdecode.Preview()` 解码。

**验证**:

- ✅ `go test ./...` 全部通过。
- ✅ `npm test` 25 项全部通过。
- ✅ `npm run build` 通过，仅有原有大 chunk 警告。
- ✅ `git diff --check` 通过。
- ⚠️ `go test -race` 未执行：当前环境缺少 `gcc`，无法启用 CGO race detector。

本次同步更新已写入此记录，随后作为一次提交推送至 GitHub `master`。

---

## USSD/USSI 与 Telegram 更新同步（2d023a7 → 0333083）

**上游提交**:

| # | Commit | 描述 |
|---|--------|------|
| 1 | 027379d | FIX #172：统一网页和 Telegram 的 USSD/USSI 路由 |
| 2 | 0333083 | Telegram 未配置 admin_id 时跳过 getUpdates 命令轮询 |

**同步范围**:

- 目标范围为 `git diff 2d023a7..0333083`，净变化 22 个文件，新增 1105 行、删除 189 行。
- `027379d` 补丁可完整应用；`0333083` 仅在 `internal/server/telegram_bot_test.go` 与本地 WAP Push 测试发生 imports 冲突，已人工合并。
- 保留本地 Telegram WAP Push、SendDTMF fake、PCSC IMEI、设备 ID 匹配、DITO/Provisioned IMSI、代理路由和 E911 `WithSIM` 修改。

**同步实现**:

- 新增 `internal/server/ussd.go`，网页和 Telegram 共用 USSD 发起、继续、取消逻辑。
- IMS/USSI 使用持久化配置 ID，蜂窝 USSD 使用当前物理设备 ID；VoWiFi 模式下 IMS 不可用时不再回退到 AT+CUSD。
- USSI token 绑定精确 SIP dialog；取消失败保留 token 和取消状态，成功或确认关闭后释放。
- `CancelUSSI` 在 IMS 不可用、runtime 缺失或 dialog 已关闭时返回明确错误，不再静默报告成功。
- Telegram 在 `admin_id` 为空时停止调用 `getUpdates`，避免无意义的高频轮询。

**额外兼容修正**:

- USSI 服务端 token 增加 15 分钟 TTL，查询和新建会话时清理过期 token。
- USSD 响应增加 `channel` 字段，前端可区分 `local`、`cellular` 和 `ims`。
- 同一 USSI dialog 的继续输入和取消使用 per-dialog 锁串行化，取消开始后不会与菜单输入交错。
- USSI 不可用错误统一返回 `ussi_not_ready`，会话不存在统一返回 `ussd_session_not_found`。
- 新增 token 过期、transport channel、并发 continue/cancel、Telegram Admin ID 和 WAP Push 回归测试。

**验证**:

- `go test ./...` 全部通过。
- `npm test` 29 项全部通过。
- `npm run build` 通过，仅有原有大 chunk 警告。
- `git diff --check` 通过。

本次同步通过 `conflict-analysis` 分支提交 PR，全部检查通过后合并到 GitHub `master`。

---

## 2026-10-22 同步上游 0333083 → 06e94ed（9 次提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 05750b3 | perf(sms): speed up cellular SMS notification delivery (#174) | SMS URC 即时唤醒、原始 PDU 保留和通知快速投递 |
| 2 | 20c8170 | build(deps): bump source-map-js (#175) | 前端间接依赖 `source-map-js` 升至 1.2.2 |
| 3 | ad369c3 | build(deps): bump postcss-selector-parser (#176) | 清理 Tailwind 3 时代的间接依赖 |
| 4 | 162f7b6 | FIX #177 | SMS 原始记录、解码失败保留、诊断接口和自动清理边界 |
| 5 | e6eba35 | Implement native QMI SIM hot-swap recovery and improve device polling logic | Native QMI SIM 热插拔恢复、WiFi410 轮询和设备后端重绑定 |
| 6 | e6e7d49 | fix(web): restore compatible Tailwind build dependencies | 恢复兼容的 Tailwind 构建依赖 |
| 7 | 07868c6 | build(deps): bump postcss-selector-parser (#178) | 再次清理旧 PostCSS/Tailwind 间接依赖 |
| 8 | 2143377 | FIX #180 | REGISTER Contact/Instance-ID 策略和注册响应 Contact 选择 |
| 9 | 06e94ed | Merge branch 'master' of https://github.com/MengMengCode/VoNet | 合并提交，无独立文件遗漏 |

**同步范围**:

- 功能补丁范围为 `git diff 0333083..06e94ed`，共涉及 51 个路径。
- 已合入 48 个功能/测试/UI 路径；`web/package.json`、`web/package-lock.json`、`web/pnpm-lock.yaml` 保留本地安全依赖基线。
- 本地已使用 `source-map-js 1.2.2`、`tailwindcss 4.3.3`、`@tailwindcss/postcss 4.3.3` 和 `vite 7.3.7`，覆盖本次依赖更新目标，且 `npm ls` 不再包含 `postcss-selector-parser`。
- `cmd/vonet/main.go`、`internal/server/call_notifications.go`、`internal/store/store.go` 存在本地兼容逻辑，已人工合并；其余上游 hunk 已合入。

**冲突和兼容处理**:

- 数据库版本由本地 31 升至 32；上游位于 migration 27 的 `sms_raw_records` 调整为本地 migration 32，并保留既有 `ensureProxyRouteSchema`。
- `pollDeviceSnapshots` 合入 Native QMI/ WiFi410 快速探测，同时保留完整发现和 `configureDeviceBackends` 重绑定。
- `reconcileCardPolicies` 将 SIM 热插拔恢复扩展到 USB SIM Reader 和 WiFi410，同时保留 PCSC Reader 回退匹配。
- 电话轮询同时跳过 WiFi410 和 PCSC Reader，避免用 `AT+CLCC` 污染无蜂窝呼叫接口设备的健康状态。
- SMS 原始记录与解码记录解耦；自动清理只删除已安全归档并成功解码的 modem 副本。

**英国 O2 Wi-Fi Calling 保护确认**:

- `ipcc-o2-23410` 的 ICCID fallback、禁用 PANI、600000 秒注册期限、`Supported`、`Allow` 和 feature tags 保留。
- Carrier Profile 校验继续允许最长 604800 秒注册期限，没有被上游的 Contact 修改收紧。
- 新增 Contact/Instance-ID 策略默认保持原行为；`carrier_overrides.json` 仅配置 Lebara UK，不影响 O2。
- O2 的 `dialogContactHeaders`、INVITE 响应 `Record-Route` 回传、`Session-Expires: ...;refresher=uas` 和受保护 TCP 来源兼容保留。
- O2 AMR/AMR-WB payload、FMTP、RTP/RTCP 路由和来电 2xx/ACK 生命周期未被本次上游更新覆盖。

**敏感信息处理**:

- 上游新增 Contact 策略测试中的疑似真实 IMEI/IMSI 已替换为明确合成测试值，测试名称不再标记为 `real_IMEI`。
- 多语言 README 中的加密货币钱包地址及其捐赠表格已删除。
- 旧 EC20 MCC/MNC 映射测试中的顺序式 IMSI 已替换为明确合成值。
- 当前待发布文件未发现密钥、令牌、密码、钱包地址或私钥；抓包脚本和数据继续由 ignore 规则排除。

**验证**:

- `go test -p 1 -count=1 ./...` 全部通过。
- `go test -p 1 -count=1 ./internal/vowifi/ims ./internal/vowifi ./internal/device ./internal/server ./internal/store` 通过，包含 O2 Contact、REGISTER、呼叫和迁移回归。
- `npm test` 29 项全部通过。
- `npm run build` 通过。
- Linux amd64 `CGO_ENABLED=0 go build` 通过。
- `git diff --check` 通过，无冲突标记。

本次同步内容后续按要求提交至 GitHub `master`。

---

## 2026-10-22 GitHub master 对比与提交

**对比基准**:

- GitHub `springai/VoNet` 最新 `master`：`a4e0509441b0c03ab3c1e16ca67328ddc531acf8`。
- 本地 `HEAD` 与 GitHub 最新提交一致，本次差异为上游 9 次提交同步、测试脱敏、README 敏感信息清理和本记录。

**本地功能保护核对**:

- 英国 O2 Wi-Fi Calling 的 `call_runtime.go`、`rtp_media.go`、`sms_runtime.go` 未被同步覆盖；REGISTER、PRACK、ACK、Record-Route、Session-Expires、AMR/AMR-WB、RTP/RTCP 和 DTMF 修复保留。
- O2 Carrier Profile、ICCID fallback、禁用 PANI、600000 秒注册期限和 604800 秒兼容上限保留。
- PCSC HardwareKind、随机 IMEI、IMEI 池/租约、DJI/Baiwang 匹配、AUTH_FAILED 轮换和 MBN/Provisioned IMSI 逻辑保留。
- E911/IKE、代理多路由、LAN bypass、USSD/USSI、WAP Push/MMS、Telegram 通知和音频修复保留。
- `AT+CNMI` 初始化已从 WiFi410 专用块泛化到所有设备并在每次 SMS 扫描时重新应用；`ConcatSMSReadyToNotify` 和通知退避语义保留。
- 数据库本地迁移、`ensureProxyRouteSchema` 和 PCSC 兼容表保留；新增 `sms_raw_records` 使用 migration 32。

**提交范围**:

- 提交本次上游同步涉及的 Go、Web、测试和文档修改。
- 保留并排除 `captures/`、`dist/`、`scripts/capture-*`、`scripts/decrypt-o2-esp.go`、`scripts/vonet_remote.py` 及其他本地抓包/构建产物。
- 测试中的疑似真实 IMEI/IMSI 使用合成值，README 多语言版本中的加密货币钱包地址已删除。

**验证**:

- `go test -p 1 -count=1 ./...` 通过。
- O2/IMS、device、server、store 相关回归通过。
- `npm test`、`npm run build` 通过。
- `git diff --check` 通过，无冲突标记。

本次同步内容提交目标为 GitHub `master`。

---

## 2026-10-22 同步上游 06e94ed → 4859412（FIX #181，选择性同步）

**同步决策**:

- 合入 `4859412` 中仍缺失的设备发现过滤、WWAN 异常标记、迟到状态写入保护和旧
  worker entry 防护。
- 等价接口和等价实现采用 VoNet 本地方案：保留 `VoWiFiDeviceRemover`、`Remove`、
  `watchDone`、`WithSIM` 和按需 `Ensure`，不新增 `VoWiFiUnregisterController` 或
  `Manager.Unregister`。
- `d8951f5 feat: initialize web frontend project with Vite, React, and Tailwind CSS
  dependencies` 有意跳过。该提交会将 Tailwind 4 临时降级为 Tailwind 3，与 VoNet
  当前 `@tailwindcss/postcss` 和 Tailwind 4 CSS 配置不兼容。
- `adcd478 build(deps): bump postcss-selector-parser (#182)` 不产生文件修改。它用于
  清理 `d8951f5` 引入的 Tailwind 3 间接依赖；VoNet 当前已经是 Tailwind `4.3.3`、
  Vite `7.3.7`，且两个 lockfile 均不包含 `postcss-selector-parser`。

**逐文件同步结果**:

| 文件 | 操作 | 说明 |
|---|---|---|
| `cmd/vonet/main.go` | 人工合并 | 自动建档和快照轮询跳过异常、无 AT 的非 PCSC 设备；保留 PCSC Lease、HardwareKind、IMEI 和后端重绑定逻辑 |
| `internal/modem/discovery.go` | 合入 | WWAN 无 AT 口设置 `at_port_missing`；保留 USB、ML307、DJI/Baiwang 本地发现逻辑 |
| `internal/vowifi/integration/store.go` | 合入 | 仅忽略设备已删除导致的迟到 runtime 写入 |
| `internal/vowifi/runtime/manager.go` | 人工合并 | 保留本地 `Remove`，补 pending 状态清理和旧 entry worker 防护 |
| `internal/server/device_api.go` | 无代码修改 | 本地 `Remove` 调用与上游删除前清理等价 |
| `internal/server/server.go` | 无代码修改 | 保留本地 `VoWiFiDeviceRemover` 接口 |
| `web/package*.json`、`web/*lock*` | 无代码修改 | 跳过 `d8951f5`，`adcd478` 目标状态已满足 |
| 相关 `_test.go` | 补充 | 覆盖 PCSC 豁免、WWAN 标记、删除清理、迟到保存、真实保存错误和旧 worker 停止 |

**本地功能保护确认**:

- PCSC HardwareKind、随机 IMEI、IMEI 池/Lease、`Reset`、`Remove` 和 `WithSIM` 保留。
- EAP/AUTH_FAILED 重试间隔、首次启动错开和重试 jitter 保留。
- O2 VoWiFi、SIP/RTP、音频、代理、诊断页面和开发者模式 UI 不在本次改动范围内。
- `@tailwindcss/postcss`、Tailwind 4 `@import "tailwindcss"` 配置和 `vonet-web` 项目名称保留。

**验证**:

- `go test -p 1 -count=1 ./...` 全部通过。
- `npm test` 29 项全部通过。
- `npm run build` 通过。
- `git diff --check` 通过，无冲突标记。

本次仅完成本地同步和记录更新，不提交、不推送 GitHub。

---

## 2026-10-27 同步审计补漏

- 补全 `README.md` 与 `docs/README.zh-CN.md` 中 OpenWrt `VONET_NFT` host wrapper、
  挂载参数和 ARMv7 musl loader 示例，命名统一为 VoNet。
- 将 `web/package-lock.json` 的 `set-cookie-parser` 与 `web/pnpm-lock.yaml`
  统一为 `2.7.2`，消除锁文件传递依赖漂移。
- `npm ci`、29 项前端测试、`npm run build` 和 `git diff --check` 均通过。

本次仅修复审计遗漏，不提交、不推送 GitHub。

---

## 2026-10-27 同步上游 4859412 → 2c1e283（9 个提交，选择性同步）

**同步范围**:

- `7aec04c` OpenWrt fw4/Docker IMS input 规则。
- `a172a66` SMS SCTS 运营商时区修正。
- `ac39db9` 上游合并提交。
- `4fb0410` 自动发布累计变更阈值和恢复流程。
- `a18dceb` linux/arm PC/SC backend。
- `0488a7a` SOCKS5 UDP 端口浮动时重建 association。
- `57e1fec` native QMI eUICC profile 切换后 provisioning 重绑和 doctor 争用检查。
- `4bb200c` `react-router-dom` 7.18.4。
- `2c1e283` `lucide-react` 1.61.0。

**逐文件同步结果**:

| 文件/区域 | 操作 | 说明 |
|---|---|---|
| `internal/vowifi/ike/*`、`Dockerfile`、README | 人工合并 | 引入 `VONET_NFT`、fw4 探测诊断、`nftables` 运行时依赖和 OpenWrt 文档；保留 VoNet 命名 |
| `internal/vowifi/sms_timestamp.go`、carrier profile、IMS/server SMS | 人工合并 | DITO SCTS 规范化、原始时间证据和入站/状态报告修正；保留 O2 SMS 外发逻辑 |
| `cmd/vonet/main.go`、`internal/store/*` | 人工合并 | delivery report 保存 `service_center_timestamp_correction`，消息 API 返回修正证据 |
| `internal/device/*` | 人工合并 | eUICC profile switch/power-cycle 后先 deactivate 再 activate Primary GW provisioning；保留 slot 1，因为现有 eUICC/CAT 链路固定使用该槽位 |
| `cmd/vonet/doctor*` | 人工合并 | 增加 OpenStick native QMI 与 ModemManager 争用检查；查询失败不会误报为通过 |
| `internal/pcsc/backend*` | 合入 | build tags 增加 linux/arm |
| `internal/vowifi/ike/transport.go` | 合入 | UDP/4500 浮动先建立新 SOCKS5 association，再关闭旧 association |
| `.github/workflows/auto-release.yml`、`scripts/auto-release.py`、测试 | 人工合并 | 使用成功 release 作为基线、失败恢复和产物校验；无稳定 release 时保留 root-commit 基线 |
| `web/package.json`、两份 lockfile、`web/src/types.ts` | 人工合并 | 只升级 `react-router-dom`/`lucide-react`，保留 Tailwind 4；增加时间修正字段 |
| `internal/vowifi/ike/userspace_firewall*.go` | 补充 | 清理旧 `vocat-ims-*` 规则，避免升级后残留 fw4 放行规则 |

**本地功能保护**:

- O2 SMS 修复、VoWiFi 通话/短信运行时、PCSC 生命周期、设备移除、无 tag 自动发布 bootstrap 和 Go 工具链修复均未被覆盖。
- 抓包脚本、抓包数据、`captures/`、`.env*` 和本地构建产物继续由 `.gitignore` 排除，不纳入同步或提交。
- QMI provisioning 保持 slot 1；多 QMI slot 需要从 `GetSlotStatus` 到 UIM/CAT/power-cycle 全链路单独设计，不能只改重绑接口。

**验证**:

- `go test ./...` 通过。
- `npm test`、`npm run build` 通过。
- `python3 -m unittest discover -s scripts -p 'test_auto_release.py' -v` 通过。
- `git diff --check` 通过。

本次仅完成本地同步和记录更新，不提交、不推送 GitHub。

---

## 2026-10-31 同步上游 f979e60 → d1968d1（6 个提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | f979e60 | feat(notifications): bind each Bark URL to its own devices (#198) | Bark `url_scopes`、按 URL 选择设备、测试与前端模型 |
| 2 | a6c0c75 | feat: API Token 认证与 MCP 端点 (#194) | `vonet_` API Token、Bearer 认证、MCP 工具接口 |
| 3 | 635e84f | fix(auth): enforce read-only access and coordinate MCP eSIM switches | 只读 Token 边界、审计、MCP/eSIM 共享切换协调 |
| 4 | 33ecd4c | fix(device): isolate eSIM recovery and supervise QMI proxy | eSIM 恢复门、QMI proxy 子进程、remoteproc 观测 |
| 5 | 01087de | feat(modem): typed AT capability profiles and unknown-USB discovery | AT Profile、APDU 方言、ZXIC 能力、未知 USB 探测 |
| 6 | d1968d1 | fix(ike): sanitize SOCKS5 relay lookup failures | SOCKS5 relay 域名错误脱敏 |

**同步范围与兼容处理**:

- Bark 改用 `url_scopes: [{url, device_ids}]` 列表，保留 URL 大小写敏感性；未知、重复、过期 scope 会被清理，非法 scope 失败关闭。短信、来电和自动任务共用目标解析，测试通知仍向草稿中的所有 URL 发送。
- Bark 多目标投递逐 URL 隔离失败并隐藏含 key 的 URL；运行时部分成功不触发整组重复重试，设置页测试要求所有目标成功。原 Web 通知设置模型、设备范围控件和多语言文案保持 VoNet 现有风格。
- API Token 只保存 SHA-256 digest，原始 `vonet_` token 仅创建时显示一次；只读 Token 在路由前拒绝写操作，审计不记录 token。MCP 挂载默认关闭，仅 `VONET_MCP_ENABLED=true` 启用，只读实例不注册写工具。
- 二进制更新完成后通过独立超时上下文原子撤销全部会话和 API Token，不受客户端断开影响；CLI Token TTL 与 Web/API 统一为最长 8760 小时，只读 `GET /api/sms/thread` 的已读副作用在 README 明确披露。
- 数据库迁移使用本地空闲版本 `33`，保留 `schemaVersion=33` 和既有 SMS/代理/IMEI 表结构。MCP 与网页 Token 管理不改变管理员会话权限。
- eSIM/QMI 部分保留本地 `Remove`、`WithSIM`、`watchDone`、slot 1 Primary GW provisioning、自动任务重连保护；只补恢复隔离、维护深度、operation epoch、managed qmi-proxy 回收和 remoteproc owner 观测，不终止共享系统 proxy。
- card-policy reconciler 的三类自动重连增加 `idle/failed` phase guard，活跃的 `*_ready` 状态不会被打断；qmi-proxy 自有进程标记统一为 `vonet-*`。
- AT 能力层保留 PCSC、ML307、DJI/Baiwang、WWAN、IMEI Lease 和 USBPath/IMEI 匹配；未知 USB AT 探测默认关闭，`VONET_PROBE_UNKNOWN_USB=true` 才启用。ZXIC 只在已验证能力集上使用 APDU/身份命令。
- IKE 查询失败只保留安全错误分类，不回显 SOCKS5 relay 域名、resolver 详情或原始错误链；本地 UDP/500→UDP/4500 float、ProxyRoute 和 UDP relay 逻辑保持不变。

**本地功能保护**:

- 英国 O2 VoWiFi、PANI、SMS over IMS、AMR/AMR-WB、DTMF、来电弹窗、SCTS 修正和 multipart `retry_safe` 逻辑未被覆盖。
- SMS 限流仍为开发者模式可配置的整数下限；VoNet 保持原有“不按国家封禁目标号码”语义。API、Telegram、自动任务和 MCP 共用该策略，因此不会因同步而拒绝 `+86` 或其他运营商号码。
- PCSC HardwareKind、随机 IMEI、IMEI 池/Lease、DJI/Baiwang IMEI/USBPath 匹配、DITO 身份改写、设备删除生命周期和自动任务连接保护均保留。
- 抓包脚本、抓包数据、`.env*`、真实 SIM/设备/号码和本地构建产物不进入同步记录或提交。

**验证**:

- `go test ./...` 通过。
- `go vet ./...` 通过。
- `npm test`：31/31 通过。
- `npm run build` 通过。
- Linux amd64、arm64、armv7（`CGO_ENABLED=0`）交叉编译通过。
- `git diff --check` 通过。

本次仅完成本地同步和记录更新，不提交、不推送 GitHub。

---

## 2026-11-04 同步上游 849c362 → 384a447（5 个提交）

**提交列表**:

| # | Commit | 描述 | 同步重点 |
|---|--------|------|----------|
| 1 | 849c362 | feat(device): add AT-backed cellular host data with policy-routed leases | AT PDP、udhcpc lease、fwmark 路由、AT/QMI 切换和恢复 |
| 2 | 9e87021 | feat(vowifi): add hardened ZXIC AT adapter support | 共享 AT core、ZXIC dialect、CLCC capability |
| 3 | 4ad7662 | feat(auth): add SSO foundation and enforce JWKS key operations | OIDC/OAuth2、JWKS `key_ops`、SSO store |
| 4 | fb59ba6 | feat(auth): add SSO login and settings integration | SSO 登录、管理员身份绑定、设置页和浏览器测试 |
| 5 | 384a447 | fix(sso): patch JOSE security alerts and clarify redirect guards | go-jose 4.1.7、JWKS/redirect 安全修复 |

**同步与兼容处理**:

- AT host data 使用前台 BusyBox `udhcpc`、60 秒首次租约和受保护的 IPv4/DNS/fwmark 路由；续租保持 rule，清理按 interface owner 执行。
- `SetNetwork` 先探测并停用活动 PDP，再配置 PDP；AT/QMI backend、ICCID、physicalID 或 generation 变化会关闭旧 session 并创建新 generation。
- Stop、设备发现、radio reset、eSIM/profile reset、MBN 选择和 developer cleanup 统一调用双后端清理；MBN 变更前必须先释放 host/QMI session。
- 共享 AT core 保留 `PureAirplanePolicy`、`HomePLMN`、`RestoreCellularData=false`、ISIM strict、typed `ExecuteAPDU` 和 EC20 错误别名。
- ZXIC 仅对验证过的 `19d2:0536` 启用；adapter binding/checkpoint 使用 deviceID、USB generation 和 ICCID 隔离，操作期间固定 physical ID/generation。
- `SupportsCLCC` 接入网页通话、来电轮询和 Telegram 操作；不支持 CLCC 的 ZXIC 不会重复占用控制通道。
- SSO 使用 VoNet migration 34/schema 34，保留 migration 33 的 API Token 和全部本地表。`go-jose` 固定为 4.1.7。
- SSO 默认不改变密码登录；只有配置并绑定外部身份后才可登录。SSO 设置只接受浏览器 Session + CSRF，Bearer/API Token 不能管理。
- 共享 redirect 向量同时约束 Go 和前端；拒绝 `//`、`/\`、编码歧义、控制字符和递归登录路径。
- 凭据 epoch 阻止管理员重置/更新撤销后迟到的 Session/API Token 写入；更新撤销同时清理 Session、API Token 和 pending SSO flow。
- 前端只增量加入 SSOCard、登录按钮、ZXIC 类型和安全 redirect，保留 VoNet 重构 UI、全局来电弹窗和 SMS 限流“大于等于 1”规则。

**部署要求**:

- AT host data 需要 Linux、iproute2、BusyBox `udhcpc`、接口独占和 IPv4；Docker/systemd 需要相应网络权限。
- SSO 生产部署需要可信 HTTPS IdP、精确 `PublicOrigin`、`VONET_SECURE_COOKIES=true` 和根路径回调。
- schema 34 升级前备份 SQLite、WAL/SHM、TLS、插件和 `/etc/vonet/env`；恢复备份后必须撤销旧凭据并清理 pending flow。
- `docker-compose.sso-test.yml`、SSO CI、Playwright 1.64.1、两份 lockfile 和忽略规则已同步为 VoNet 命名。

**验证**:

- `go test ./...` 通过。
- `npm test`：47/47 通过。
- `npm run test:e2e`：7/7 通过；`docker compose -f docker-compose.sso-test.yml config` 通过。
- `npm run build`、`go vet ./...`、Linux amd64/arm64/armv7 交叉编译通过。
- `git diff --check` 通过。

本次仅完成本地同步和记录更新，不提交、不推送 GitHub。
