package requester

import (
	"net/http"
	"one-api/common/utils"
	"time"
)

var HTTPClient *http.Client

func InitHttpClient() {
	// 从配置读取连接池参数，提供合理的默认值
	maxIdleConns := utils.GetOrDefault("max_idle_conns", 200)
	maxIdleConnsPerHost := utils.GetOrDefault("max_idle_conns_per_host", 50)
	maxConnsPerHost := utils.GetOrDefault("max_conns_per_host", 100)
	idleConnTimeout := utils.GetOrDefault("idle_conn_timeout", 300)

	trans := &http.Transport{
		DialContext: utils.Socks5ProxyFunc,
		Proxy:       utils.ProxyFunc,
		// 连接池配置 - 支持高并发
		MaxIdleConns:        maxIdleConns,        // 全局最大空闲连接数
		MaxIdleConnsPerHost: maxIdleConnsPerHost, // 每个主机最大空闲连接数
		MaxConnsPerHost:     maxConnsPerHost,     // 每个主机最大连接数
		// 超时配置 - 支持长时间连接
		IdleConnTimeout:       time.Duration(idleConnTimeout) * time.Second, // 空闲连接超时
		TLSHandshakeTimeout:   30 * time.Second,  // TLS握手超时
		ExpectContinueTimeout: 5 * time.Second,   // Expect: 100-continue超时
		ResponseHeaderTimeout: 60 * time.Second,  // 响应头超时
		// 启用HTTP/2以提高性能
		ForceAttemptHTTP2: true,
		// 禁用连接压缩以减少CPU使用
		DisableCompression: false,
	}

	HTTPClient = &http.Client{
		Transport: trans,
	}

	// 设置总体请求超时，默认150秒以支持120秒+的请求
	relayTimeout := utils.GetOrDefault("relay_timeout", 150)
	if relayTimeout > 0 {
		HTTPClient.Timeout = time.Duration(relayTimeout) * time.Second
	}
}
