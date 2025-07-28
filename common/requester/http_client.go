package requester

import (
	"net/http"
	"one-api/common/utils"
	"time"
	
	"github.com/bytedance/gopkg/util/gopool"
)

var HTTPClient *http.Client

func InitHttpClient() {
	// 配置goroutine池，防止goroutine耗尽
	gopool.SetCap(1000)      // 设置goroutine池容量为1000
	gopool.SetMaxIdle(100)   // 设置最大空闲goroutine数为100
	
	// 获取超时配置
	connectTimeout := utils.GetOrDefault("connect_timeout", 5)
	idleConnTimeout := utils.GetOrDefault("idle_conn_timeout", 90)
	
	trans := &http.Transport{
		DialContext:           utils.Socks5ProxyFunc,
		Proxy:                 utils.ProxyFunc,
		MaxIdleConns:          100,                                           // 最大空闲连接数
		MaxIdleConnsPerHost:   20,                                            // 每个主机的最大空闲连接数
		IdleConnTimeout:       time.Duration(idleConnTimeout) * time.Second,  // 空闲连接超时
		TLSHandshakeTimeout:   10 * time.Second,                              // TLS握手超时
		ExpectContinueTimeout: 1 * time.Second,                               // Expect: 100-continue 超时
		ResponseHeaderTimeout: time.Duration(connectTimeout) * time.Second,   // 响应头超时
		DisableKeepAlives:     false,                                         // 启用Keep-Alive
		ForceAttemptHTTP2:     true,                                          // 强制尝试HTTP/2
	}

	HTTPClient = &http.Client{
		Transport: trans,
	}

	relayTimeout := utils.GetOrDefault("relay_timeout", 0)
	if relayTimeout > 0 {
		HTTPClient.Timeout = time.Duration(relayTimeout) * time.Second
	}
}
