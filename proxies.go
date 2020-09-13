package httpproxyfailover

import (
	"bufio"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"sync"
)

type Proxies []string

func (ps Proxies) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodConnect:
		ps.connect(w, r)
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

func (ps Proxies) connect(w http.ResponseWriter, r *http.Request) {
	_, _ = ioutil.ReadAll(r.Body)
	r.Body.Close()

	for _, p := range ps {
		u, err := url.Parse(p)
		if err != nil {
			continue
		}

		inbound, err := net.Dial("tcp", u.Host)
		if err != nil {
			continue
		}

		if err := r.Write(inbound); err != nil {
			continue
		}

		br := bufio.NewReader(inbound)
		resp, err := http.ReadResponse(br, r)
		if err != nil {
			continue
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			continue
		}

		h := w.(http.Hijacker)
		outbound, _, err := h.Hijack()
		if err != nil {
			panic(err)
		}

		_ = resp.Write(outbound)
		b, _ := br.Peek(br.Buffered())
		_, _ = outbound.Write(b)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer inbound.Close()

			_, _ = io.Copy(inbound, outbound)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer outbound.Close()

			_, _ = io.Copy(outbound, inbound)
		}()
		wg.Wait()

		return
	}

	http.Error(w, "", http.StatusServiceUnavailable)
}
