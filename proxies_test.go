package httpproxyfailover

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProxies_ServeHTTP(t *testing.T) {
	t.Run("CONNECT", func(t *testing.T) {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("origin"))
			assert.NoError(t, err)
		}))
		originURL, err := url.Parse(origin.URL)
		assert.NoError(t, err)
		defer origin.Close()

		proxy1Status := http.StatusOK
		proxy1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(proxy1Status)
		}))
		defer proxy1.Close()

		proxy2Status := http.StatusOK
		proxy2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(proxy2Status)
		}))
		defer proxy2.Close()

		l, err := net.Listen("tcp", ":0")
		assert.NoError(t, err)
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				assert.NoError(t, conn.Close())
			}
		}()

		t.Run("empty", func(t *testing.T) {
			h := Proxies{}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		})

		t.Run("OK", func(t *testing.T) {
			h := Proxies{proxy1.URL, proxy2.URL}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("invali URL", func(t *testing.T) {
			h := Proxies{":non-url", proxy2.URL}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("inaccessible proxy", func(t *testing.T) {
			h := Proxies{"http://localhost:0/", proxy2.URL}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("non-responsive proxy", func(t *testing.T) {
			h := Proxies{fmt.Sprintf("http://%s/", l.Addr()), proxy2.URL}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("unavailable proxy", func(t *testing.T) {
			proxy1Status = http.StatusServiceUnavailable
			defer func() {
				proxy1Status = http.StatusOK
			}()
			h := Proxies{proxy1.URL, proxy2.URL}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	})

	t.Run("Other methods", func(t *testing.T) {
		h := Proxies{}

		w := newRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

type recorder struct {
	*httptest.ResponseRecorder
}

func newRecorder() recorder {
	return recorder{
		ResponseRecorder: httptest.NewRecorder(),
	}
}

func (r recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return &conn{}, nil, nil
}

type conn struct {
}

func (c *conn) Read(b []byte) (n int, err error) {
	return 0, io.EOF
}

func (c *conn) Write(b []byte) (n int, err error) {
	return 0, nil
}

func (c *conn) Close() error {
	return nil
}

func (c *conn) LocalAddr() net.Addr {
	panic("implement me")
}

func (c *conn) RemoteAddr() net.Addr {
	panic("implement me")
}

func (c *conn) SetDeadline(t time.Time) error {
	panic("implement me")
}

func (c *conn) SetReadDeadline(t time.Time) error {
	panic("implement me")
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	panic("implement me")
}
