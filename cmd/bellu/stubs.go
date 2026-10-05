package main

import (
	"context"
	"fmt"
)

// These subcommands are wired in later tasks. They are declared here so the
// CLI dispatches cleanly from the start.

func runNormalize(ctx context.Context, args []string) error {
	return normalizeCmd(ctx, args)
}

func runResolve(ctx context.Context, args []string) error {
	return resolveCmd(ctx, args)
}

func runSync(ctx context.Context, args []string) error {
	return syncCmd(ctx, args)
}

func runAll(ctx context.Context, args []string) error {
	return runAllCmd(ctx, args)
}

func runExplain(ctx context.Context, args []string) error {
	return explainCmd(ctx, args)
}

// notImplemented is a placeholder for subcommands whose implementation lands in
// a later task.
func notImplemented(name string) error {
	return fmt.Errorf("%s: not implemented yet", name)
}
