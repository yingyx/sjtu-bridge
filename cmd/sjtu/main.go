package main

import (
	"context"
	"github.com/yingyx/sjtu-bridge/internal/adapters/cli"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}
