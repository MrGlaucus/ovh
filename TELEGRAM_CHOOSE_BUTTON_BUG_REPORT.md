# Telegram「选择账户下单」按钮 Loading 问题诊断报告

## 1. 结论

**最可能根因**: 在监控有货通知发送时，`SetTelegramButtonAccount()` 调用失败或被跳过，导致数据库中的按钮记录 `account_id` 字段为空字符串。当用户点击「选择账户下单」按钮时，`CompatibleOrderAccounts(\"\")` 函数返回空或不足 2 个账户，导致 `showTelegramAccountChoices()` 返回 false，无法展示账户选择界面。

**次要可能根因**: 
- Telegram Webhook 的 secret_token 验证失败，导致回调请求被拒绝且未调用 `answerCallbackQuery`
- `decodeCallbackData()` 解析失败，导致未调用 `answerCallbackQuery`

**严重程度**: 高 - 核心功能完全失效

## 2. 症状与发现对应

### 症状 1: 点击「XXX 机房选择账户下单」后 Telegram 按钮一直 loading

**共享原因**: `answerCallbackQuery` 未被调用

**触发路径**:
1. 用户点击按钮 → Telegram 显示加载动画
2. 服务器处理回调但未调用 `AnswerCallback()` → 加载动画持续
3. 约 30 秒后 Telegram 自动超时停止加载

**最可能位置**:
- `handlers/telegram.go:271-273`: `refuseInLegacyMode()` 检查失败直接返回，未调用 `AnswerCallback`
- `handlers/telegram.go:301-305`: `decodeCallbackData()` 失败直接返回，未调用 `AnswerCallback`
- `handlers/telegram.go:101-106`: Webhook secret 验证失败返回 401，未调用 `AnswerCallback`

**预期结果**: 无论成功或失败，`handleTelegramCallback()` 中的「choose」分支都调用了 `AnswerCallback()`，理论上不应出现永久 loading

### 症状 2: 随后无变化，未进入账户选择

**共享原因**: `showTelegramAccountChoices()` 返回 false

**触发路径**:
1. `showTelegramAccountChoices()` 检索按钮记录
2. 调用 `CompatibleOrderAccounts(row.AccountID)`
3. 如果返回账户数 < 2，函数返回 false
4. 不调用 `EditMessageReplyMarkup()`，原按钮保持可见
5. 调用 `AnswerCallback()` 显示错误提示

**最可能位置**: `monitor/notify.go:346-350` 或 `db/telegram_buttons.go:158-167`

**预期结果**: 调用 `EditMessageReplyMarkup()` 更新消息为账户选择按钮列表

## 3. 根本原因分析

### 缺陷 1: SetTelegramButtonAccount() 可能被跳过或失败 [代码缺陷]

**位置**: `server/internal/monitor/notify.go:346-350`

**机制**:
```go
if btnAccountID != "" && m.state.DB != nil {
    if err := m.state.DB.SetTelegramButtonAccount(msgUUID, btnAccountID); err != nil {
        m.state.Logger.Warn("一键下单按钮账户归属落库失败...", "telegram")
    }
}
```

**问题**:
- 条件 `btnAccountID != "" && m.state.DB != nil` 任一为假则跳过 UPDATE
- 即使失败也仅记录警告，不影响后续流程
- 按钮记录保持 `account_id = ""`

**证据**:
- 代码注释明确说明"失败只退回'默认账户'的老行为，不影响按钮本身可用"
- 但这对「选择账户下单」按钮是致命问题，因为需要明确的 account_id 来过滤区域匹配账户

**证伪条件**: 如果所有「选择账户下单」按钮都能正常工作，则此非根因

### 缺陷 2: CompatibleOrderAccounts(\"\") 的行为不符合预期 [设计缺陷]

**位置**: `server/internal/monitor/notify.go:217-232`

**机制**:
```go
func (m *Monitor) CompatibleOrderAccounts(referenceAccountID string) []types.OVHAccount {
    reference, ok := m.state.FindAccount(referenceAccountID)
    if !ok {
        return nil  // 空 ID 时 FindAccount 返回默认账户，这里不会返回 nil
    }
    region := ovh.SubsidiaryRegion(catalog.SubsidiaryOfAccount(reference))
    // 过滤同一区域的账户
}
```

**问题**:
- 当 `referenceAccountID = ""` 时，`FindAccount("")` 返回默认账户
- 按默认账户的区域过滤，可能导致返回账户数 < 2
- 例如：默认账户在 US 区只有 1 个账户，但实际有多个 EU 区账户

**证据**:
- `app/app.go:200-220` 中 `FindAccount("")` 的实现逻辑
- `catalog/region.go:28-34` 中 `SubsidiaryOfAccount()` 的逻辑

**证伪条件**: 如果默认账户所在区域始终有 ≥2 个账户，则此非根因

### 缺陷 3: AnswerCallback 可能在关键路径前被跳过 [时序缺陷]

**位置**: `server/internal/handlers/telegram.go:101-106`

**机制**:
```go
okSecret, legacy := telegram.ValidateWebhookSecret(state, c.GetHeader(telegram.SecretTokenHeader))
if !okSecret {
    state.Logger.Warn("拒绝 secret_token 无效的 webhook 请求...")
    c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid_secret_token"})
    return  // 未调用 AnswerCallback
}
```

**问题**:
- Webhook secret 验证失败直接返回 401
- 未调用 `answerCallbackQuery`，Telegram 端按钮持续 loading
- 直到 Telegram 30 秒超时自动取消

**证据**:
- `telegram/security.go:91-106` 中 `ValidateWebhookSecret()` 的返回值逻辑
- 如果 `!WebhookSecretEnforced(state)` 返回 false 且无法获取 secret，返回 `(false, false)`

**证伪条件**: 如果所有回调都受此影响（包括「一键下单」），而用户仅报告「选择账户下单」有问题，则此非根因

### 缺陷 4: decodeCallbackData 失败未调用 AnswerCallback [健壮性缺陷]

**位置**: `server/internal/handlers/telegram.go:301-305`

**机制**:
```go
callbackObj, ok := decodeCallbackData(state, cbData)
if !ok {
    c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Invalid callback data format"})
    return  // 未调用 AnswerCallback
}
```

**问题**:
- JSON 解析失败直接返回 400
- 未调用 `answerCallbackQuery`，Telegram 端按钮持续 loading

**证据**:
- 当前代码中无任何异常保护
- 如果 callback_data 被截断或损坏，会导致永久 loading

**证伪条件**: 如果从未发生过 callback_data 解析失败的情况，则此非根因

## 4. 其他观察

1. **child 按钮创建使用父按钮的 createdAt 时间戳**: `showTelegramAccountChoices:247` 中使用 `row.CreatedAt` 而非当前时间。如果父按钮已接近 TTL 过期，child 按钮可能立即过期。

2. **缺少并发保护**: `showTelegramAccountChoices` 不消费原按钮，允许用户多次点击。虽然不会造成重复下单，但可能导致多个 child 按钮同时创建。

3. **日志级别不足**: `SetTelegramButtonAccount` 失败仅记录 Warn，建议升级为 Error 并考虑阻断通知发送。

## 5. 修复建议

### 优先修复 (P0)

1. **确保 SetTelegramButtonAccount 执行成功**:
   ```go
   // notify.go:346-350
   if btnAccountID != "" && m.state.DB != nil {
       if err := m.state.DB.SetTelegramButtonAccount(msgUUID, btnAccountID); err != nil {
           m.state.Logger.Error("一键下单按钮账户归属落库失败："+err.Error(), "telegram")
           // 考虑在此场景下阻止通知发送，避免按钮不可用
           return
       }
   }
   ```

2. **Handle empty account_id gracefully**:
   ```go
   // notify.go:335-339
   if btnAccountID == "" {
       m.state.Logger.Error("一键下单按钮无法解析账户归属，将不使用账户选择功能："+planCode, "monitor")
       // 单账户用户或订阅没勾自动下单是正常的，但多账户情况下应报错
       if len(m.CompatibleOrderAccounts("")) >= 2 {
           m.state.Logger.Warn("检测到多账户但仍无法解析账户归属", "monitor")
       }
   }
   ```

3. **添加 AnswerCallback 保护**:
   ```go
   // telegram.go:301-305
   callbackObj, ok := decodeCallbackData(state, cbData)
   if !ok {
       telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "按钮数据异常", true)
       c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Invalid callback data format"})
       return
   }
   ```

### 次要修复 (P1)

4. **改进 CompatibleOrderAccounts 的空 ID 处理**:
   ```go
   // monitor.go:217-232
   func (m *Monitor) CompatibleOrderAccounts(referenceAccountID string) []types.OVHAccount {
       if referenceAccountID == "" {
           // 空 ID 时返回所有账户，由调用方决定如何过滤
           m.state.AccountsMu.RLock()
           defer m.state.AccountsMu.RUnlock()
           accounts := make([]types.OVHAccount, len(m.state.Accounts))
           copy(accounts, m.state.Accounts)
           return accounts
       }
       // ... 原有逻辑
   }
   ```

5. **增加调试日志**:
   - 在 `showTelegramAccountChoices` 各关键步骤添加 Debug 日志
   - 记录 `row.AccountID`、`len(accounts)`、`EditMessageReplyMarkup` 返回值

### 验证方案

#### 方案 A: 本地复现测试
1. 配置 2+ 个同区域 OVH 账户
2. 手动创建测试订阅指向特定账户
3. 模拟有货通知，检查数据库中 `telegram_order_buttons.account_id` 是否正确写入
4. 点击「选择账户下单」按钮，观察：
   - 数据库是否创建 child 按钮
   - Telegram 是否收到 `answerCallbackQuery`
   - 按钮是否正确更新

#### 方案 B: 生产环境日志分析
1. 搜索最近 7 天的 Telegram 相关日志
2. 查找包含「一键下单按钮账户归属落库失败」的警告
3. 统计「选择账户下单」回调的成功/失败比例
4. 检查是否有「invalid_secret_token」或「unauthorized_actor」错误

#### 方案 C: 单元测试补充
```go
// monitor/notify_test.go
func TestCompatibleOrderAccounts_EmptyReference(t *testing.T) {
    state := testState(t)
    // 添加 2 个 EU 账户 + 1 个 US 账户
    // 测试 CompatibleOrderAccounts("") 的行为
    
    mon := monitor.New(state)
    accounts := mon.CompatibleOrderAccounts("")
    if len(accounts) < 2 {
        t.Errorf("Expected >= 2 accounts for empty reference, got %d", len(accounts))
    }
}

// handlers/telegram_test.go
func TestShowTelegramAccountChoices_EmptyAccountID(t *testing.T) {
    state := testState(t)
    mon := monitor.New(state)
    
    // 创建 account_id = "" 的按钮
    buttonID := uuid.NewString()
    state.DB.UpsertTelegramButton(buttonID, "24ska01", "ynm", []string{}, nil, time.Now().Unix())
    
    cb := map[string]interface{}{
        "message": map[string]interface{}{
            "chat": map[string]interface{}{"id": int64(12345)},
            "message_id": float64(67890),
        },
    }
    
    result := showTelegramAccountChoices(state, mon, cb, buttonID)
    // 预期：account_id = "" 时应降级为使用默认账户或返回 false
    _ = result
}
```

## 6. 下一步行动

1. **立即**: 检查生产环境最近 7 天 Telegram 日志，确认是否存在 `SetTelegramButtonAccount` 失败记录
2. **24 小时内**: 部署 P0 修复，特别是添加 `AnswerCallback` 保护
3. **48 小时内**: 完成单元测试覆盖，防止回归
4. **1 周内**: 考虑重构 `CompatibleOrderAccounts` 接口，明确空 ID 的语义

---

**诊断人**: Debug Agent  
**诊断时间**: 2026-09-09  
**参考提交**: 963970b feat: select accounts for telegram orders
