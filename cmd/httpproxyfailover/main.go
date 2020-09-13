package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ichiban/httpproxyfailover"
	"github.com/spf13/pflag"
)

func main() {
	var port int

	pflag.IntVarP(&port, "port", "p", 0, "port number")
	pflag.Parse()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)

	s := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: httpproxyfailover.Proxies(pflag.Args()),
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
