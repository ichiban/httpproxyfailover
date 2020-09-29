package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ichiban/httpproxyfailover"
	"github.com/spf13/pflag"
)

func main() {
	var port int
	var timeout int
	var verbose bool

	pflag.IntVarP(&port, "port", "p", 0, "specify port number")
	pflag.IntVarP(&timeout, "timeout", "t", 0, "set timeout (millisecond)")
	pflag.BoolVarP(&verbose, "verbose", "v", false, "show logs")
	pflag.Parse()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)

	p := httpproxyfailover.Proxy{
		Backends: pflag.Args(),
		Timeout:  time.Duration(timeout) * time.Millisecond,
	}

	if verbose {
		p.Callback = func(r *http.Request, b string, err error) {
			msg := "success"
			if err != nil {
				msg = err.Error()
			}
			log.Printf("from:%s\tto:%s\tvia:%s\tresult:%s", r.RemoteAddr, r.RequestURI, b, msg)
		}
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
