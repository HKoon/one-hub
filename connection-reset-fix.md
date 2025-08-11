# Connection Reset by Peer 错误修复方案

## 问题分析

"connection reset by peer" 错误是网络连接被上游服务器重置导致的，主要原因包括：

1. **错误识别不完整**：原有的 `ErrorWrapper` 函数只检测 "Post" 和 "dial" 错误，未识别 "connection reset by peer" 等网络连接错误
2. **重试逻辑缺陷**：网络连接错误被标记为 `LocalError`，导致无法重试
3. **超时配置不合理**：默认的重试超时时间过短（10秒）

## 已实施的修复

### 1. 增强错误识别机制

修改了 `common/gin.go` 中的 `ErrorWrapper` 函数，新增对以下网络错误的识别：

```go
// 检测各种网络连接错误
if strings.Contains(errString, "Post") || 
   strings.Contains(errString, "dial") ||
   strings.Contains(errString, "connection reset by peer") ||
   strings.Contains(errString, "connection refused") ||
   strings.Contains(errString, "timeout") ||
   strings.Contains(errString, "EOF") ||
   strings.Contains(errString, "broken pipe") {
    logger.SysError(fmt.Sprintf("network error: %s", errString))
    errString = "请求上游地址失败"
}
```

### 2. 优化重试逻辑

修改了 `relay/common.go` 中的 `shouldRetry` 函数：

- **网络错误特殊处理**：即使是 `LocalError`，如果是网络连接错误也允许重试
- **超时错误重试**：对于 504、524 等超时错误，如果是网络连接问题则允许重试
- **500错误重试**：将 500 错误视为网络连接问题，允许重试

### 3. 修正LocalError标记

修改了 `ErrorWrapperLocal` 函数，确保网络连接错误不会被错误地标记为 `LocalError`：

```go
// 只有非网络错误才标记为LocalError
if !isNetworkError {
    openaiErr.LocalError = true
}
```

## 推荐配置

### 环境变量配置

```bash
# 重试配置
RETRY_TIMES=3              # 重试次数：3次
RETRY_TIME_OUT=600         # 重试总超时：10分钟
RETRY_COOLDOWN_SECONDS=10  # 重试间隔：10秒

# 连接配置
CONNECT_TIMEOUT=30         # 连接超时：30秒
RELAY_TIMEOUT=180          # 中继超时：3分钟

# HTTP客户端优化
HTTP_IDLE_CONN_TIMEOUT=90     # 空闲连接超时：90秒
HTTP_MAX_IDLE_CONNS=100       # 最大空闲连接数：100
HTTP_MAX_IDLE_CONNS_PER_HOST=10  # 每个主机最大空闲连接数：10
```

### 配置说明

1. **重试次数（3次）**：足够处理临时网络波动，但不会过度消耗资源
2. **重试超时（600秒）**：为长时间请求提供足够的重试窗口
3. **重试间隔（10秒）**：给上游服务器恢复时间，避免立即重试加重负载
4. **连接超时（30秒）**：比默认5秒更宽松，减少连接超时
5. **中继超时（180秒）**：适合处理较长的AI模型响应时间

## 验证方法

1. **查看日志**：观察是否出现 "network error:" 前缀的日志，确认网络错误被正确识别
2. **监控重试**：检查重试日志，确认网络错误能够触发重试
3. **错误统计**：统计 "请求上游地址失败" 错误的频率变化
4. **响应时间**：监控平均响应时间和成功率的改善

## 长期优化建议

1. **指数退避重试**：实现指数退避算法，避免重试风暴
2. **连接池优化**：配置HTTP客户端连接池参数
3. **熔断机制**：实现熔断器模式，快速失败避免级联故障
4. **健康检查**：定期检查渠道健康状态
5. **监控告警**：建立连接错误监控和告警机制

## 注意事项

- 修改后需要重启服务才能生效
- 建议在测试环境先验证修复效果
- 监控服务器资源使用情况，确保重试不会导致资源耗尽
- 根据实际网络环境调整超时和重试参数