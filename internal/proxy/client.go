package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

var proxyClientCache sync.Map

type proxyClientKey struct {
	timeoutS  float64
	verifySSL bool
}

func getOrCreateProxyClient(timeoutS float64, verifySSL bool) *http.Client {
	key := proxyClientKey{timeoutS: timeoutS, verifySSL: verifySSL}
	if cached, ok := proxyClientCache.Load(key); ok {
		return cached.(*http.Client)
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutS * float64(time.Second)),
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !verifySSL,
			},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
	actual, _ := proxyClientCache.LoadOrStore(key, client)
	return actual.(*http.Client)
}
