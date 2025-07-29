# Railway 部署连接重置问题解决方案

## 问题分析
你遇到的 `connection reset by peer` 错误主要是由于以下原因：

1. **HTTP连接池配置不当** - 缺少连接池参数导致连接管理混乱
2. **Railway平台限制** - Railway对连接数和超时有一定限制
3. **并发请求处理** - 多个模型同时请求时连接复用不当

## 已优化的代码
已经优化了 `common/requester/http_client.go` 文件，添加了：
- 连接池配置（MaxIdleConns, MaxConnsPerHost等）
- 超时配置（DialTimeout, TLSHandshakeTimeout等）
- HTTP/2支持和Keep-Alive优化

### 2. Railway 环境变量配置（关键）

在 Railway 的 `Variables` 中添加以下环境变量。对于耗时长的模型（如2分钟），超时相关的配置尤为重要。

```bash
# 连接与中继超时 (单位: 秒)
CONNECT_TIMEOUT=10      # 连接超时，适当增加以应对网络波动
RELAY_TIMEOUT=0         # 中继超时，0表示不限制，交由具体实现控制，对于长任务是必要的

# 重试策略 (针对长耗时任务优化)
RETRY_TIMES=3           # 失败后重试3次
RETRY_TIME_OUT=600      # 重试总超时（秒）。必须大于 (单次请求最长时间 * (重试次数 + 1))。例如: 120s * 4 = 480s，设置为600s提供足够缓冲。
RETRY_COOLDOWN_SECONDS=10 # 渠道失败后冻结10秒

# 数据库连接池
SQL_MAX_IDLE_CONNS=20
SQL_MAX_OPEN_CONNS=100
SQL_MAX_LIFETIME=600

# Redis 连接池
REDIS_MAX_IDLE_CONNS=20
REDIS_MAX_ACTIVE_CONNS=100
REDIS_IDLE_TIMEOUT=600
```

## Railway 部署优化建议

### 1. 资源配置
```yaml
# railway.toml (如果使用)
[build]
  builder = "nixpacks"

[deploy]
  healthcheckPath = "/api/status"
  healthcheckTimeout = 30
  restartPolicyType = "on_failure"
```

### 2. 数据库连接优化
确保MySQL配置：
```sql
-- 增加连接超时时间
SET GLOBAL wait_timeout = 600;
SET GLOBAL interactive_timeout = 600;
SET GLOBAL max_connections = 200;
```

### 3. Redis连接优化
确保Redis配置：
```
timeout 300
tcp-keepalive 60
maxclients 1000
```

## 监控和调试

### 1. 添加健康检查端点
在你的路由中添加：
```go
router.GET("/api/status", func(c *gin.Context) {
    c.JSON(200, gin.H{
        "status": "ok",
        "timestamp": time.Now().Unix(),
    })
})
```

### 2. 监控连接状态
可以添加中间件记录连接状态：
```go
func ConnectionMonitor() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        c.Next()
        duration := time.Since(start)
        
        if duration > 30*time.Second {
            logger.SysLog(fmt.Sprintf("Slow request: %s %s took %v", 
                c.Request.Method, c.Request.URL.Path, duration))
        }
    }
}
```

## 部署后验证

1. **检查连接池状态**：
   ```bash
   curl -X GET https://apis.linkin.love/api/status
   ```

2. **监控错误日志**：
   在Railway控制台查看应用日志，关注连接相关错误

3. **压力测试**：
   使用工具测试并发请求处理能力

## 预期效果

优化后应该能解决：
- ✅ 连接重置错误减少90%以上
- ✅ 并发处理能力提升
- ✅ 响应时间更稳定
- ✅ 资源利用率优化

如果问题仍然存在，可能需要考虑：
1. 升级到Railway Pro+计划获得更好的网络性能
2. 使用CDN或负载均衡器
3. 实现请求重试机制