package downloader

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

// defaultTransport is a downloader-tuned http.Transport shared across
// all Download calls that don't bring their own HTTPClient.
//
// Tuning rationale:
//
//   - MaxIdleConnsPerHost is bumped well above the stdlib default of 2
//     so multi-part chunk workers don't repeatedly tear down and
//     re-establish TCP+TLS to the same CDN host.
//
//   - HTTP/2 is intentionally disabled (NextProtos = http/1.1). HTTP/2
//     would multiplex every chunk worker onto a single TCP connection,
//     which lets the origin's per-connection rate limit cap the whole
//     download. Forcing HTTP/1.1 makes each worker open its own TCP
//     stream, which on CDNs like Cloudfront (HuggingFace) typically
//     yields 3-10x aggregate throughput on home/mobile networks.
//
//   - DisableCompression is true: model weights (.gguf, .safetensors)
//     are already incompressible, so gzip just burns CPU on the client
//     and prevents the server from streaming raw bytes.
//
//   - Read/Write buffers are enlarged to reduce syscall overhead on
//     fast links.
var (
	defaultTransportOnce sync.Once
	defaultTransport     *http.Transport
	defaultClient        *http.Client
)

func sharedTransport() *http.Transport {
	defaultTransportOnce.Do(func() {
		defaultTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			// Force HTTP/1.1 so concurrent chunks each get their own
			// TCP connection instead of being multiplexed onto one h2
			// stream (which the origin can rate-limit as a single
			// connection).
			ForceAttemptHTTP2: false,
			TLSClientConfig: &tls.Config{
				NextProtos: []string{"http/1.1"},
			},
			TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   64,
			MaxConnsPerHost:       0,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ReadBufferSize:        1 << 20, // 1 MiB
			WriteBufferSize:       64 << 10,
			// gguf / safetensors are already incompressible; skip gzip
			// to save CPU and avoid blocking range optimizations.
			DisableCompression: true,
		}
		defaultClient = &http.Client{Transport: defaultTransport}
	})
	return defaultTransport
}

// sharedHTTPClient returns the package-level http.Client backed by
// sharedTransport. Callers that don't inject their own client via
// Options.HTTPClient pick this up automatically.
func sharedHTTPClient() *http.Client {
	sharedTransport()
	return defaultClient
}
