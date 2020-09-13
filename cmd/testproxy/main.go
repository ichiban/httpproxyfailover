package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/pflag"
)

func main() {
	var port int
	var fail bool

	pflag.IntVarP(&port, "port", "p", 0, "port number")
	pflag.BoolVarP(&fail, "fail", "f", false, "fail")
	pflag.Parse()

	s := http.Server{
		Addr: fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				http.Error(w, "", http.StatusMethodNotAllowed)
				return
			}

			log.Printf("CONNECT %s", r.URL.Host)

			_, _ = ioutil.ReadAll(r.Body)
			r.Body.Close()

			if fail {
				http.Error(w, "", http.StatusBadGateway)
				return
			}

			inbound, err := net.Dial("tcp", r.URL.Host)
			if err != nil {
				http.Error(w, "", http.StatusBadGateway)
				return
			}

			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			h := w.(http.Hijacker)
			outbound, _, err := h.Hijack()
			if err != nil {
				panic(err)
			}

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

				_, _ = io.Copy(outbound, inbound)
			}()
		}),
	}

	go func() {
		err := s.ListenAndServe()
		switch err {
		case nil, http.ErrServerClosed:
		default:
			panic(err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)
	<-c

	if err := s.Shutdown(context.Background()); err != nil {
		panic(err)
	}
}
