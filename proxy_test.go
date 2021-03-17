package httpproxyfailover

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
			if credentials(r) != "proxy1:proxy1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Connection", "close")
			w.WriteHeader(proxy1Status)
		}))
		defer proxy1.Close()
		proxy1URL := func(username, password string) string {
			u, err := url.Parse(proxy1.URL)
			assert.NoError(t, err)
			u.User = url.UserPassword(username, password)
			return u.String()
		}

		proxy2Status := http.StatusOK
		proxy2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if credentials(r) != "proxy2:proxy2" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Connection", "close")
			w.WriteHeader(proxy2Status)
		}))
		defer proxy2.Close()
		proxy2URL := func(username, password string) string {
			u, err := url.Parse(proxy2.URL)
			assert.NoError(t, err)
			u.User = url.UserPassword(username, password)
			return u.String()
		}

		t.Run("empty", func(t *testing.T) {
			var c MockCallback
			defer c.AssertExpectations(t)

			h := Proxy{
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		})

		t.Run("OK", func(t *testing.T) {
			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy1URL("proxy1", "proxy1"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{proxy1URL("proxy1", "proxy1"), proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("OK with templates enabled", func(t *testing.T) {
			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{"{foo}" + strings.Replace(proxy1URL("proxy1", "proxy1"), "proxy", "{proxy}", -1), "{bar}" + strings.Replace(proxy2URL("proxy2", "proxy2"), "proxy", "{proxy}", -1)},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}
			assert.NoError(t, h.EnableTemplates())

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("bar,proxy=proxy")))
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("auth error", func(t *testing.T) {
			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy1URL("invalid", "invalid"), mock.MatchedBy(func(err error) bool {
				e, ok := err.(*unsuccessfulStatusError)
				if !ok {
					return false
				}

				return e.statusCode == http.StatusUnauthorized
			})).Return()
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{proxy1URL("invalid", "invalid"), proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("invalid URL", func(t *testing.T) {
			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), ":non-url", mock.MatchedBy(func(err error) bool {
				e, ok := err.(*url.Error)
				if !ok {
					return false
				}

				return e.Op == "parse"
			})).Return()
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{":non-url", proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("inaccessible proxy", func(t *testing.T) {
			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), "http://localhost:0/", mock.MatchedBy(func(err error) bool {
				e, ok := err.(*net.OpError)
				if !ok {
					return false
				}

				return e.Op == "dial"
			})).Return()
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{"http://localhost:0/", proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("non-responsive proxy", func(t *testing.T) {
			l, err := net.Listen("tcp", ":0")
			assert.NoError(t, err)
			defer func() {
				assert.NoError(t, l.Close())
			}()

			go func() {
				for {
					conn, err := l.Accept()
					if err != nil {
						return
					}
					assert.NoError(t, conn.Close())
				}
			}()

			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), fmt.Sprintf("http://%s/", l.Addr()), mock.MatchedBy(func(err error) bool {
				switch err := err.(type) {
				case *net.OpError:
					return err.Op == "read"
				default:
					return err == io.ErrUnexpectedEOF
				}
			})).Return()
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{fmt.Sprintf("http://%s/", l.Addr()), proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

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

			var c MockCallback
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy1URL("proxy1", "proxy1"), mock.MatchedBy(func(err error) bool {
				e, ok := err.(*unsuccessfulStatusError)
				if !ok {
					return false
				}

				return e.statusCode == http.StatusServiceUnavailable
			})).Return()
			c.On("OnConnect", mock.AnythingOfType("*http.Request"), proxy2URL("proxy2", "proxy2"), nil).Return()
			c.On("OnDisconnect", int64(0), int64(0)).Return()
			defer c.AssertExpectations(t)

			h := Proxy{
				Backends:     []string{proxy1URL("proxy1", "proxy1"), proxy2URL("proxy2", "proxy2")},
				OnConnect:    c.OnConnect,
				OnDisconnect: c.OnDisconnect,
			}

			w := newRecorder()
			r := httptest.NewRequest(http.MethodConnect, originURL.Host, nil)
			h.ServeHTTP(w, r)
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	})

	t.Run("Other methods", func(t *testing.T) {
		var c MockCallback
		defer c.AssertExpectations(t)

		h := Proxy{
			OnConnect:    c.OnConnect,
			OnDisconnect: c.OnDisconnect,
		}

		w := newRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("TLS handshake", func(t *testing.T) {
		origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NoError(t, r.Body.Close())
			_, err := w.Write([]byte("origin"))
			assert.NoError(t, err)
		}))
		defer origin.Close()

		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodConnect, r.Method)
			assert.NoError(t, r.Body.Close())

			inbound, err := net.Dial("tcp", r.URL.Host)
			assert.NoError(t, err)

			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			h, ok := w.(http.Hijacker)
			assert.True(t, ok)

			outbound, _, err := h.Hijack()
			assert.NoError(t, err)

			pipe(inbound, outbound)
		}))
		defer backend.Close()

		t.Run("OK", func(t *testing.T) {
			tls := TLS
			TLS = *origin.Client().Transport.(*http.Transport).TLSClientConfig
			defer func() {
				TLS = tls
			}()

			proxy := httptest.NewServer(&Proxy{
				Backends: []string{backend.URL},
				Checks:   []Check{CheckTLSHandshake},
			})
			defer proxy.Close()

			proxyURL, err := url.Parse(proxy.URL)
			assert.NoError(t, err)

			c := origin.Client()
			transport, ok := c.Transport.(*http.Transport)
			assert.True(t, ok)
			transport.Proxy = http.ProxyURL(proxyURL)

			resp, err := c.Get(origin.URL)
			assert.NoError(t, err)
			assert.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("NG", func(t *testing.T) {
			proxy := httptest.NewServer(&Proxy{
				Backends: []string{backend.URL},
				Checks:   []Check{CheckTLSHandshake},
			})
			defer proxy.Close()

			proxyURL, err := url.Parse(proxy.URL)
			assert.NoError(t, err)

			c := origin.Client()
			transport, ok := c.Transport.(*http.Transport)
			assert.True(t, ok)
			transport.Proxy = http.ProxyURL(proxyURL)

			_, err = c.Get(origin.URL)
			assert.Error(t, err)
		})
	})

	t.Run("favicon", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodConnect, r.Method)
			assert.NoError(t, r.Body.Close())

			inbound, err := net.Dial("tcp", r.URL.Host)
			assert.NoError(t, err)

			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			h, ok := w.(http.Hijacker)
			assert.True(t, ok)

			outbound, _, err := h.Hijack()
			assert.NoError(t, err)

			pipe(inbound, outbound)
		}))
		defer backend.Close()

		t.Run("OK", func(t *testing.T) {
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.NoError(t, r.Body.Close())
				_, err := w.Write([]byte("origin"))
				assert.NoError(t, err)
			}))
			defer origin.Close()

			proxy := httptest.NewServer(&Proxy{
				Backends: []string{backend.URL},
				Checks:   []Check{CheckFavicon},
			})
			defer proxy.Close()

			proxyURL, err := url.Parse(proxy.URL)
			assert.NoError(t, err)

			c := origin.Client()
			c.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)

			tls := TLS
			TLS = *c.Transport.(*http.Transport).TLSClientConfig
			defer func() {
				TLS = tls
			}()

			resp, err := c.Get(origin.URL)
			assert.NoError(t, err)
			assert.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("NG", func(t *testing.T) {
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h, ok := w.(http.Hijacker)
				assert.True(t, ok)
				conn, _, err := h.Hijack()
				assert.NoError(t, err)
				conn.Close()
			}))
			defer origin.Close()

			proxy := httptest.NewServer(&Proxy{
				Backends: []string{backend.URL},
				Checks:   []Check{CheckFavicon},
			})
			defer proxy.Close()

			proxyURL, err := url.Parse(proxy.URL)
			assert.NoError(t, err)

			c := origin.Client()
			c.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)

			_, err = c.Get(origin.URL)
			assert.Error(t, err)
		})
	})
}

func TestProxy_EnableTemplates(t *testing.T) {
	t.Run("valid URI templates", func(t *testing.T) {
		h := Proxy{
			Backends: []string{
				"{fast}http://{username}:{password}@proxy1.example.com:8080",
				"http://{username}:{password}@proxy2.example.com:8080",
				"http://{username}:{password}@proxy3.example.com:8080",
			},
		}
		assert.NoError(t, h.EnableTemplates())
	})

	t.Run("invalid URI templates", func(t *testing.T) {
		h := Proxy{
			Backends: []string{
				"{fast}http://{username}:{password}@proxy1.example.com:8080",
				"http://{username:{password}@proxy2.example.com:8080", // invalid
				"http://{username}:{password}@proxy3.example.com:8080",
			},
		}
		assert.Error(t, h.EnableTemplates())
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

type MockCallback struct {
	mock.Mock
}

func (m *MockCallback) OnConnect(r *http.Request, b string, err error) {
	m.Called(r, b, err)
}

func (m *MockCallback) OnDisconnect(read, wrote int64) {
	m.Called(read, wrote)
}

func credentials(r *http.Request) string {
	auth := r.Header.Get("Proxy-Authorization")
	fields := strings.Fields(auth)
	if len(fields) != 2 {
		return ""
	}
	if fields[0] != "Basic" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return ""
	}
	return string(b)
}
