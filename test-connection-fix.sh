#!/bin/bash

# Connection Reset Fix 测试脚本
# 用于验证网络连接错误修复效果

echo "=== One Hub 连接重置错误修复验证脚本 ==="
echo "开始时间: $(date)"
echo ""

# 检查服务是否运行
echo "1. 检查服务状态..."
if pgrep -f "one-hub" > /dev/null; then
    echo "✓ One Hub 服务正在运行"
else
    echo "✗ One Hub 服务未运行，请先启动服务"
    exit 1
fi
echo ""

# 检查日志文件
echo "2. 检查日志配置..."
LOG_DIR="./logs"
if [ -d "$LOG_DIR" ]; then
    echo "✓ 日志目录存在: $LOG_DIR"
    LATEST_LOG=$(ls -t $LOG_DIR/*.log 2>/dev/null | head -1)
    if [ -n "$LATEST_LOG" ]; then
        echo "✓ 最新日志文件: $LATEST_LOG"
    else
        echo "⚠ 未找到日志文件"
    fi
else
    echo "⚠ 日志目录不存在: $LOG_DIR"
fi
echo ""

# 检查网络错误处理
echo "3. 检查网络错误识别..."
if [ -n "$LATEST_LOG" ]; then
    # 检查最近的网络错误日志
    NETWORK_ERRORS=$(tail -1000 "$LATEST_LOG" | grep -c "network error:")
    CONNECTION_RESET=$(tail -1000 "$LATEST_LOG" | grep -c "connection reset by peer")
    RETRY_LOGS=$(tail -1000 "$LATEST_LOG" | grep -c "to retry (remain times")
    
    echo "最近1000行日志中:"
    echo "  - 网络错误识别: $NETWORK_ERRORS 次"
    echo "  - 连接重置错误: $CONNECTION_RESET 次"
    echo "  - 重试日志: $RETRY_LOGS 次"
    
    if [ $NETWORK_ERRORS -gt 0 ]; then
        echo "✓ 网络错误识别功能正常工作"
    else
        echo "ℹ 暂未发现网络错误（这可能是好事）"
    fi
else
    echo "⚠ 无法检查日志，请确保日志功能已启用"
fi
echo ""

# 检查配置
echo "4. 检查重试配置..."
echo "当前环境变量:"
echo "  RETRY_TIMES: ${RETRY_TIMES:-未设置}"
echo "  RETRY_TIME_OUT: ${RETRY_TIME_OUT:-未设置}"
echo "  RETRY_COOLDOWN_SECONDS: ${RETRY_COOLDOWN_SECONDS:-未设置}"
echo "  CONNECT_TIMEOUT: ${CONNECT_TIMEOUT:-未设置}"
echo "  RELAY_TIMEOUT: ${RELAY_TIMEOUT:-未设置}"
echo ""

# 建议配置
echo "5. 推荐配置:"
echo "如果您还没有设置以下环境变量，建议添加到您的启动脚本中:"
echo ""
echo "export RETRY_TIMES=3"
echo "export RETRY_TIME_OUT=600"
echo "export RETRY_COOLDOWN_SECONDS=10"
echo "export CONNECT_TIMEOUT=30"
echo "export RELAY_TIMEOUT=180"
echo ""

# 实时监控建议
echo "6. 实时监控建议:"
echo "您可以使用以下命令实时监控网络错误:"
echo ""
echo "# 监控网络错误:"
echo "tail -f $LOG_DIR/*.log | grep --color=always 'network error:\|connection reset\|to retry'"
echo ""
echo "# 统计错误频率:"
echo "tail -1000 $LOG_DIR/*.log | grep 'network error:' | wc -l"
echo ""

# 测试API连接
echo "7. 测试API连接..."
API_URL="http://localhost:3000"
if command -v curl > /dev/null; then
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$API_URL" --connect-timeout 10)
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "404" ] || [ "$HTTP_CODE" = "302" ]; then
        echo "✓ API服务响应正常 (HTTP $HTTP_CODE)"
    else
        echo "⚠ API服务响应异常 (HTTP $HTTP_CODE)"
    fi
else
    echo "⚠ curl命令不可用，无法测试API连接"
fi
echo ""

echo "=== 验证完成 ==="
echo "结束时间: $(date)"
echo ""
echo "如果您仍然遇到连接重置错误，请:"
echo "1. 检查上游API服务的稳定性"
echo "2. 调整重试和超时参数"
echo "3. 查看详细日志分析具体错误原因"
echo "4. 考虑联系技术支持"