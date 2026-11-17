package main

import (
	"context"
	"github.com/michibiki-io/raft-snapshot-retainer/internal/cli"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.Main(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr, version, time.Now, cli.DefaultFactory)
	stop()
	os.Exit(code)
}
