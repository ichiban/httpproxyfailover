// Package httpproxyfailover provides a means to construct a fault-tolerant HTTP proxy out of multiple somewhat
// unreliable HTTP proxies.
package httpproxyfailover

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Proxy is a proxy for backend HTTP proxies.
type Proxy struct {
	// Backends hold backend HTTP proxies. Proxy tries backend HTTP proxies in order of the slice and use the first one
	// that responds with a successful status code (2XX).
	Backends []string

	// Timeout sets the deadline of trial of each backend HTTP proxy if provided.
	Timeout time.Duration

	// TLSHandshake requires further check on each backend. If set, a backend is considered available if not only it
	// responds a CONNECT request with a successful status code (2XX) but also a TLS handshake succeeds.
	// This check occurs in a different TCP connection. So there's no guarantee that the proxy connection also succeeds
	// with a TLS handshake.
	TLSHandshake *tls.Config

	// Callback is signaled after every trial of the backend HTTP proxies if provided.
	// The first argument is the CONNECT request, the second argument is the backend HTTP proxy in trial, and the last
	// argument is the resulting error which is nil if it succeeded.
	Callback func(*http.Request, string, error)
}

func (p Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.Body.Close()
	switch r.Method {
	case http.MethodConnect:
		p.connect(w, r)
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

func (p Proxy) connect(w http.ResponseWriter, r *http.Request) {
	if p.Callback == nil {
		p.Callback = func(*http.Request, string, error) {}
	}

	for _, b := range p.Backends {
		inbound, resp, err := p.connectOne(b, w, r)
		if err != nil {
			p.Callback(r, b, err)
			continue
		}

		p.Callback(r, b, nil)

		h, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "", http.StatusBadGateway)
			return
		}

		outbound, _, err := h.Hijack()
		if err != nil {
			http.Error(w, "", http.StatusBadGateway)
			return
		}

		_ = resp.Write(outbound)
		pipe(inbound, outbound)
		return
	}

	http.Error(w, "", http.StatusServiceUnavailable)
}

func urlParse(raw string) (*url.URL, error) {
	if !strings.HasPrefix(raw, "http://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

func (p *Proxy) connectOne(b string, w http.ResponseWriter, r *http.Request) (net.Conn, *http.Response, error) {
	ctx := r.Context()
	if p.Timeout != 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(r.Context(), p.Timeout)
		defer cancel()
	}

	inbound, resp, err := p.inbound(ctx, r, b)
	if err != nil {
		return nil, nil, err
	}

	if p.TLSHandshake != nil {
		if err := p.checkTLSHandshake(ctx, r, b); err != nil {
			return nil, nil, err
		}
	}

	return inbound, resp, nil
}

func pipe(inbound, outbound net.Conn) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			_ = inbound.Close()
		}()

		_, _ = io.Copy(inbound, outbound)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			_ = outbound.Close()
		}()

		_, _ = io.Copy(outbound, inbound)
	}()
	wg.Wait()
}

func (p *Proxy) checkTLSHandshake(ctx context.Context, r *http.Request, b string) error {
	i, _, err := p.inbound(ctx, r, b)
	if err != nil {
		return err
	}

	target := url.URL{Host: r.RequestURI}
	config := *p.TLSHandshake
	config.ServerName = target.Hostname()
	conn := tls.Client(i, &config)
	defer func() {
		_ = conn.Close()
	}()

	return conn.Handshake()
}

func (p *Proxy) inbound(ctx context.Context, r *http.Request, b string) (net.Conn, *http.Response, error) {
	u, err := urlParse(b)
	if err != nil {
		return nil, nil, err
	}

	var d net.Dialer
	inbound, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, nil, err
	}

	req := backendReq(r, u.User)
	if err := req.Write(inbound); err != nil {
		return nil, nil, err
	}

	br := bufio.NewReader(inbound)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		return nil, nil, err
	}
	_ = resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, nil, &unsuccessfulStatusError{
			statusCode: resp.StatusCode,
			status:     resp.Status,
		}
	}

	return inbound, resp, nil
}

func backendReq(r *http.Request, userinfo *url.Userinfo) *http.Request {
	req := http.Request{
		Method: http.MethodConnect,
		URL: &url.URL{
			Host: r.Host,
		},
		Header: http.Header{},
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	if userinfo != nil {
		req.Header.Set("Proxy-Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(userinfo.String()))))
	}
	return &req
}

type unsuccessfulStatusError struct {
	statusCode int
	status     string
}

func (err *unsuccessfulStatusError) Error() string {
	if err.status != "" {
		return err.status
	}
	return fmt.Sprintf("%d %s", err.statusCode, http.StatusText(err.statusCode))
}
