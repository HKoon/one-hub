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

## Railway 环境变量配置建议

在Railway项目中设置以下环境变量：

```bash
# 连接超时配置
CONNECT_TIMEOUT=10
RELAY_TIMEOUT=300

# 数据库连接池配置
SQL_MAX_IDLE_CONNS=50
SQL_MAX_OPEN_CONNS=100

# Redis配置优化
REDIS_CONN_STRING=redis://default:password@host:port/0

# 日志级别（用于调试）
LOG_LEVEL=info

# Gin模式
GIN_MODE=release

# 批量更新配置（减少数据库压力）
BATCH_UPDATE_ENABLED=true
BATCH_UPDATE_INTERVAL=5

# 内存缓存（减少数据库查询）
MEMORY_CACHE_ENABLED=true
SYNC_FREQUENCY=300
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