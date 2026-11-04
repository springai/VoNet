package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"vonet/internal/store"
)

// runAuthCleanup removes restored or stale authentication state without
// changing the SSO provider configuration or external binding.
func runAuthCleanup(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("auth-cleanup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databasePath := flags.String("database", "/opt/vonet/data/vonet.db", "database path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: vonet auth-cleanup [--database path]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := store.Open(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.DeleteAllAuthCredentials(ctx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "sessions, API tokens, and pending SSO flows revoked")
	return nil
}
