package httpproxyfailover

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
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
		u, err := url.Parse(b)
		if err != nil {
			p.Callback(r, b, err)
			continue
		}

		inbound, err := net.DialTimeout("tcp", u.Host, p.Timeout)
		if err != nil {
			p.Callback(r, b, err)
			continue
		}

		if err := r.Write(inbound); err != nil {
			p.Callback(r, b, err)
			continue
		}

		br := bufio.NewReader(inbound)
		resp, err := http.ReadResponse(br, r)
		if err != nil {
			p.Callback(r, b, err)
			continue
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			p.Callback(r, b, &UnsuccessfulStatusError{
				StatusCode: resp.StatusCode,
				Status:     resp.Status,
			})
			continue
		}

		p.Callback(r, b, nil)

		h := w.(http.Hijacker)
		outbound, _, err := h.Hijack()
		if err != nil {
			http.Error(w, "", http.StatusBadGateway)
			return
		}

		_ = resp.Write(outbound)
		b, _ := br.Peek(br.Buffered())
		_, _ = outbound.Write(b)

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

		return
	}

	http.Error(w, "", http.StatusServiceUnavailable)
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
