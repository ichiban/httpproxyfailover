package httpproxyfailover

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Proxy is a proxy for backend HTTP proxies.
type Proxy struct {
	// Backends hold backend HTTP proxies. The Proxy tries backend HTTP proxies in order of the slice and use the first one that responds with a successful status code (2XX).
	Backends []string

	// Timeout sets the deadline of trial of each backend HTTP proxy if provided.
	Timeout time.Duration

	// Callback is signaled after every trial of the backend HTTP proxies if provided.
	// The first argument is the CONNECT request, the second argument is the backend HTTP proxy in trial, and the last argument is the resulting error which is nil if it succeeded.
	Callback func(*http.Request, string, error)
}

func (p Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodConnect:
		p.connect(w, r)
	default:
		_, _ = ioutil.ReadAll(r.Body)
		_ = r.Body.Close()
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

func (p Proxy) connect(w http.ResponseWriter, r *http.Request) {
	if p.Callback == nil {
		p.Callback = func(*http.Request, string, error) {}
	}

	_, _ = ioutil.ReadAll(r.Body)
	_ = r.Body.Close()

	for _, b := range p.Backends {
		if p.connectOne(b, w, r) {
			return
		}
	}

	http.Error(w, "", http.StatusServiceUnavailable)
}

func urlParse(raw string) (*url.URL, error) {
	if !strings.HasPrefix(raw, "http://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

func (p *Proxy) connectOne(b string, w http.ResponseWriter, r *http.Request) bool {
	ctx := r.Context()
	if p.Timeout != 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(r.Context(), p.Timeout)
		defer cancel()
	}

	u, err := urlParse(b)
	if err != nil {
		p.Callback(r, b, err)
		return false
	}

	var d net.Dialer
	inbound, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		p.Callback(r, b, err)
		return false
	}

	req := backendReq(r, u.User)
	if err := req.Write(inbound); err != nil {
		p.Callback(r, b, err)
		return false
	}

	br := bufio.NewReader(inbound)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		p.Callback(r, b, err)
		return false
	}
	_ = resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		p.Callback(r, b, &UnsuccessfulStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		})
		return false
	}

	p.Callback(r, b, nil)

	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "", http.StatusBadGateway)
		return true
	}

	outbound, _, err := h.Hijack()
	if err != nil {
		http.Error(w, "", http.StatusBadGateway)
		return true
	}

	_ = resp.Write(outbound)

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

	return true
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

type UnsuccessfulStatusError struct {
	StatusCode int
	Status     string
}

func (err *UnsuccessfulStatusError) Error() string {
	if err.Status != "" {
		return err.Status
	}
	return fmt.Sprintf("%d %s", err.StatusCode, http.StatusText(err.StatusCode))
}
