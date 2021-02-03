package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ichiban/httpproxyfailover"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

func init() {
	pflag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [options...] <backend proxy URI template>...\n", os.Args[0])
		pflag.PrintDefaults()
	}
}

func main() {
	var port int
	var timeout time.Duration
	var tlsHandshake bool
	var favicon bool

	pflag.IntVarP(&port, "port", "p", 0, "Specify port number to listen on (random if not specified)")
	pflag.DurationVarP(&timeout, "timeout", "t", 0, "Set timeout for each trial")
	pflag.BoolVarP(&tlsHandshake, "tls", "T", false, "Check TLS handshake")
	pflag.BoolVarP(&favicon, "favicon", "f", false, "Check Favicon")
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
				log.WithError(err).Warn("fail-over")
				return
			}
			log.Info("connect")
		},
	}

	if tlsHandshake {
		p.Checks = append(p.Checks, httpproxyfailover.CheckTLSHandshake)
	}

	if favicon {
		p.Checks = append(p.Checks, httpproxyfailover.CheckFavicon)
	}

	if err := p.EnableTemplates(); err != nil {
		logrus.WithError(err).Fatal("failed to enable templates")
	}

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logrus.WithError(err).Fatal("failed to listen")
	}

	logrus.WithFields(logrus.Fields{
		"addr":         l.Addr(),
		"timeout":      timeout,
		"tlsHandshake": tlsHandshake,
		"favicon":      favicon,
	}).Info("start")

	s := http.Server{
		Handler: p,
	}

	go func() {
		err := s.Serve(l)
		switch err {
		case nil, http.ErrServerClosed:
		default:
			logrus.WithError(err).Fatal("failed to listen and serve")
		}
	}()

	<-c

	if err := s.Shutdown(context.Background()); err != nil {
		logrus.WithError(err).Fatal("failed to shutdown")
	}

	logrus.Info("end")
}
