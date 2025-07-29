package requester

import (
	"net/http"
	"one-api/common/utils"
	"time"
)

var HTTPClient *http.Client

func InitHttpClient() {
	trans := &http.Transport{
		DialContext: utils.Socks5ProxyFunc,
		Proxy:       utils.ProxyFunc,
		
		// 连接池配置
		MaxIdleConns:        100,              // 最大空闲连接数
		MaxIdleConnsPerHost: 20,               // 每个主机最大空闲连接数
		MaxConnsPerHost:     50,               // 每个主机最大连接数
		IdleConnTimeout:     90 * time.Second, // 空闲连接超时时间
		
		// 超时配置
		TLSHandshakeTimeout:   10 * time.Second,                           // TLS握手超时
		ResponseHeaderTimeout: 180 * time.Second,                          // 响应头超时 - 关键配置
		ExpectContinueTimeout: 1 * time.Second,                            // Expect: 100-continue超时
		
		// 保持连接活跃
		DisableKeepAlives: false,
		
		// 启用HTTP/2
		ForceAttemptHTTP2: true,
	}

	HTTPClient = &http.Client{
		Transport: trans,
	}

	relayTimeout := utils.GetOrDefault("relay_timeout", 0)
	if relayTimeout > 0 {
		HTTPClient.Timeout = time.Duration(relayTimeout) * time.Second
	} else {
		// 如果没有设置relay_timeout，使用默认的300秒超时
		HTTPClient.Timeout = 300 * time.Second
	}
}
