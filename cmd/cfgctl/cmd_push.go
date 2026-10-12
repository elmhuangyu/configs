package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/elmhuangyu/configs/pkg/syncer"
)

func runPush(args []string) error {
	fs := pflag.NewFlagSet("push", pflag.ContinueOnError)

	var (
		prefix    = fs.String("prefix", "", "etcd key prefix (required)")
		endpoints = fs.String("endpoints", "127.0.0.1:2379", "comma-separated etcd endpoints")
		prune     = fs.Bool("prune", false, "delete remote keys that no longer exist locally")
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cfgctl push <dir> --prefix <etcd_prefix> [--prune]")
	}
	if *prefix == "" {
		return fmt.Errorf("--prefix is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	result, err := syncer.PushDirectory(ctx, syncer.PushOptions{
		Dir:       fs.Arg(0),
		Prefix:    *prefix,
		Endpoints: splitEndpoints(*endpoints),
		Prune:     *prune,
	})
	if err != nil {
		return err
	}

	if result.Puts == 0 && result.Deletes == 0 {
		fmt.Printf("Nothing to push to %s\n", result.Prefix)
		return nil
	}
	fmt.Printf("Pushed %d put(s) and %d delete(s) to %s at revision %d\n",
		result.Puts, result.Deletes, result.Prefix, result.Revision)
	return nil
}

func splitEndpoints(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
