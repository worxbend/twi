package main

import (
	"context"
	"github.com/worxbend/twi/internal/cli"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

func main() {
	// Handle broken data pipes through command exit contracts instead of signal termination.
	signal.Ignore(syscall.SIGPIPE)
	ctx, cancel := context.WithCancel(context.Background())
	var received atomic.Int32
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	signalDone := make(chan struct{})
	go func() {
		defer close(signalDone)
		var sig os.Signal
		select {
		case sig = <-signals:
		case <-done:
			select {
			case sig = <-signals:
			default:
				return
			}
		}
		if sig == os.Interrupt {
			received.Store(130)
		} else {
			received.Store(143)
		}
		cancel()
	}()
	code := cli.RunContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	signal.Stop(signals)
	close(done)
	<-signalDone
	cancel()
	if value := received.Load(); value != 0 {
		code = int(value)
	}
	os.Exit(code)
}
