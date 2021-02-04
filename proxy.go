// Package httpproxyfailover provides a means to construct a fault-tolerant HTTP proxy out of multiple somewhat
// unreliable HTTP proxies.
package httpproxyfailover

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yosida95/uritemplate/v3"
)

// Proxy is a proxy for backend HTTP proxies.
type Proxy struct {
	// Backends hold backend HTTP proxies. Proxy tries backend HTTP proxies in order of the slice and use the first one
	// that responds with a successful status code (2XX).
	Backends       []string
	parsedBackends []*uritemplate.Template

	// Timeout sets the deadline of trial of each backend HTTP proxy if provided.
	Timeout time.Duration

	// Checks are further checks on each backend. A backend is considered available if not only it responds a CONNECT
	// request with a successful status code (2XX) but also all the check functions return no errors.
	Checks []Check

	// Callback is signaled after every trial of the backend HTTP proxies if provided.
	// The first argument is the CONNECT request, the second argument is the backend HTTP proxy in trial, and the last
	// argument is the resulting error which is nil if it succeeded.
	Callback func(connect *http.Request, backend string, err error)
}

type Check = func(ctx context.Context, connect *http.Request, backend string) error

func (p Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.Body.Close()
	switch r.Method {
	case http.MethodConnect:
		p.connect(w, r)
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

// EnableTemplates() parses backends as URI templates.
// Proxy will connect to only applicable backends which template variables are satisfied.
// The values for template variables are populated from the credentials in Proxy-Authorization header. The substring
// before the first ':' (usually considered as username) should be the form of a list of key-value pairs (`k1=v1,k2=v2`).
// Each pair is separated by '=' without whitespaces, and those pairs are separated by ',' without whitespaces.
// Optionally, you can omit '=' and the value (`k1=v1,k2=v2,tag`). Then it's considered a pair of the key and empty
// string (`k1=v1,k2=v2,tag=`).
func (p *Proxy) EnableTemplates() error {
	p.parsedBackends = make([]*uritemplate.Template, len(p.Backends))
	for i, b := range p.Backends {
		t, err := uritemplate.New(b)
		if err != nil {
			p.parsedBackends = nil
			return fmt.Errorf("%s: %w", b, err)
		}
		p.parsedBackends[i] = t
	}
	return nil
}

func (p Proxy) connect(w http.ResponseWriter, r *http.Request) {
	if p.Callback == nil {
		p.Callback = func(*http.Request, string, error) {}
	}

	backends, err := p.applicableBackends(r)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	for _, b := range backends {
		inbound, resp, err := p.connectOne(b, r)
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

func (p *Proxy) applicableBackends(r *http.Request) ([]string, error) {
	if p.parsedBackends == nil {
		return p.Backends, nil
	}

	values, err := params(r)
	if err != nil {
		return nil, err
	}

	ret := make([]string, 0, len(p.parsedBackends))
	for _, t := range p.parsedBackends {
		if !applicable(t, values) {
			continue
		}
		b, err := t.Expand(values)
		if err != nil {
			continue
		}
		ret = append(ret, b)
	}
	return ret, nil
}

func applicable(t *uritemplate.Template, values uritemplate.Values) bool {
	for _, n := range t.Varnames() {
		if _, ok := values[n]; !ok {
			return false
		}
	}
	return true
}

func params(r *http.Request) (uritemplate.Values, error) {
	const prefix = "Basic "

	auth := r.Header.Get("Proxy-Authorization")
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return nil, nil
	}

	b, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return nil, err
	}

	ps := string(b)
	if i := strings.IndexRune(ps, ':'); i >= 0 {
		ps = ps[:i]
	}

	values := uritemplate.Values{}
	for _, kv := range strings.Split(ps, ",") {
		kv := strings.SplitN(kv, "=", 2)
		if len(kv) == 1 {
			kv = append(kv, "")
		}
		values.Set(kv[0], uritemplate.Value{
			T: uritemplate.ValueTypeString,
			V: kv[1:],
		})
	}
	return values, nil
}

func urlParse(raw string) (*url.URL, error) {
	if !strings.HasPrefix(raw, "http://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

func (p *Proxy) connectOne(b string, r *http.Request) (net.Conn, *http.Response, error) {
	ctx := r.Context()
	if p.Timeout != 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(r.Context(), p.Timeout)
		defer cancel()
	}

	inbound, resp, err := inbound(ctx, r, b)
	if err != nil {
		return nil, nil, err
	}

	for _, c := range p.Checks {
		if err := c(ctx, r, b); err != nil {
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

// TLS is TLS configuration for Check.
var TLS tls.Config

// CheckTLSHandshake requires a further check on each backend. If set in Proxy.Checks, a backend which speaks TLS has to
// succeed TLS handshake.
// This check occurs in a different TCP connection. So there's no guarantee that the proxy connection also succeeds
// with a TLS handshake.
func CheckTLSHandshake(ctx context.Context, connect *http.Request, backend string) error {
	i, _, err := inbound(ctx, connect, backend)
	if err != nil {
		return err
	}

	target := url.URL{Host: connect.RequestURI}
	config := TLS
	config.ServerName = target.Hostname()
	conn := tls.Client(i, &config)
	defer func() {
		_ = conn.Close()
	}()

	if err := conn.Handshake(); err != nil {
		// It might not be a TLS server. In that case, it's okay to fail.
		if errors.As(err, &tls.RecordHeaderError{}) {
			return nil
		}
		return err
	}

	return nil
}

// CheckFavicon requires further check on each backend. If set in Proxy.Checks, a backend has to succeed a GET request
// for favicon.
func CheckFavicon(ctx context.Context, connect *http.Request, backend string) error {
	u, err := urlParse(backend)
	if err != nil {
		return err
	}

	c := http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(u),
			TLSClientConfig: &TLS,
		},
	}

	target := url.URL{
		Scheme: "https",
		Host:   connect.RequestURI,
	}
	switch target.Port() {
	case "80":
		target.Scheme = "http"
		target.Host = target.Hostname()
	case "443":
		target.Host = target.Hostname()
	}

	rootURL := target
	rootURL.Path = "/"

	faviconURL := target
	faviconURL.Path = "/favicon.ico"

	req, err := http.NewRequest(http.MethodGet, faviconURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Referer", rootURL.String())
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("User-Agent", connect.Header.Get("User-Agent"))

	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	_, _ = io.Copy(ioutil.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("favicon status: %d", resp.StatusCode)
	}

	return nil
}

func inbound(ctx context.Context, connect *http.Request, backend string) (net.Conn, *http.Response, error) {
	u, err := urlParse(backend)
	if err != nil {
		return nil, nil, err
	}

	var d net.Dialer
	inbound, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, nil, err
	}

	req := backendReq(connect, u.User)
	if err := req.Write(inbound); err != nil {
		return nil, nil, err
	}

	br := bufio.NewReader(inbound)
	resp, err := http.ReadResponse(br, connect)
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
