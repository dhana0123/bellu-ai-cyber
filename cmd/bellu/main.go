// Command bellu is the CLI for the Bellu AI Security Graph ingestion layer.
//
// Subcommands:
//
//	ingest     collect from a source into the evidence store (or stdout)
//	normalize  turn raw evidence into canonical entities and edges
//	resolve    assign canonical IDs and merge aliases
//	sync       write resolved graph into Memgraph
//	run        ingest -> normalize -> resolve -> sync
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "migrate":
		err = runMigrate(ctx, args)
	case "ingest":
		err = runIngest(ctx, args)
	case "normalize":
		err = runNormalize(ctx, args)
	case "resolve":
		err = runResolve(ctx, args)
	case "sync":
		err = runSync(ctx, args)
	case "run":
		err = runAll(ctx, args)
	case "explain":
		err = runExplain(ctx, args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "bellu: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "bellu %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bellu - AI Security Graph ingestion

usage: bellu <command> [flags]

commands:
  migrate     apply database migrations to the evidence store
  ingest      collect from a source into the evidence store (or stdout)
  normalize   turn raw evidence into canonical entities and edges
  resolve     assign canonical IDs and merge aliases
  sync        write the resolved graph into Memgraph
  run         ingest -> normalize -> resolve -> sync
  explain     print the evidence chain for an edge

run "bellu <command> -h" for command flags
`)
}
