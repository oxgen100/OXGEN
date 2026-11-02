package main

import (
	"context"
	"os"
	"os/signal"

	"oxgen/internal/oxgen"
)

var version = "0.4.1-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); stop() }()
	os.Exit(oxgen.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version))
}
