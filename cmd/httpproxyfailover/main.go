package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ichiban/httpproxyfailover"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

func main() {
	var port int
	var timeout time.Duration
	var tlsHandshake bool

	pflag.IntVarP(&port, "port", "p", 0, "specify port number")
	pflag.DurationVarP(&timeout, "timeout", "t", 0, "set timeout")
	pflag.BoolVarP(&tlsHandshake, "tls", "T", false, "check TLS handshake")
	pflag.Parse()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)

	p := httpproxyfailover.Proxy{
		Backends: pflag.Args(),
		Timeout:  timeout,
		Callback: func(r *http.Request, b string, err error) {
			log := logrus.WithFields(logrus.Fields{
				"from": r.RemoteAddr,
				"to":   r.RequestURI,
				"via":  b,
			})
			if err != nil {
				log.WithError(err).Error("NG")
				return
			}
			log.Info("OK")
		},
	}

	if tlsHandshake {
		p.TLSHandshake = &tls.Config{}
	}

	s := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: p,
	}

	go func() {
		err := s.ListenAndServe()
		switch err {
		case nil, http.ErrServerClosed:
		default:
			panic(err)
		}
	}()

	<-c

	if err := s.Shutdown(context.Background()); err != nil {
		panic(err)
	}
}
