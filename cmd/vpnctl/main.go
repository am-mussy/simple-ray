package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mussy/simple-ray/internal/app"
	"github.com/mussy/simple-ray/internal/cli"
	"github.com/mussy/simple-ray/internal/panelbootstrap"
	"github.com/mussy/simple-ray/internal/state"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) == 3 && os.Args[1] == "__panel-bootstrap" {
		if err := panelbootstrap.Run(ctx, os.Args[2], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	store := state.New("/var/lib/vpnctl")
	service := app.New(store, "/run/vpnctl/lock")
	command := &cli.CLI{Service: service, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, IsTTY: isTerminal(os.Stdout)}
	code := command.Run(ctx, os.Args[1:])
	if ctx.Err() != nil && code == 0 {
		code = 130
	}
	os.Exit(code)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
