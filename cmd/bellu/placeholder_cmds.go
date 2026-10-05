package main

import "context"

// Placeholder command bodies replaced in later tasks.
// normalizeCmd  -> Task 4
// resolveCmd    -> Task 4 (pass-through), Task 10 (full)
// syncCmd       -> Task 5
// runAllCmd     -> Task 5
// explainCmd    -> Task 12

func normalizeCmd(_ context.Context, _ []string) error { return notImplemented("normalize") }
func resolveCmd(_ context.Context, _ []string) error   { return notImplemented("resolve") }
func syncCmd(_ context.Context, _ []string) error      { return notImplemented("sync") }
func runAllCmd(_ context.Context, _ []string) error    { return notImplemented("run") }
func explainCmd(_ context.Context, _ []string) error   { return notImplemented("explain") }
