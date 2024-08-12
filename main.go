// Copyright 2024 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/tgulacsi/go/httpunix"
)

func main() {
	if err := Main(); err != nil {
		slog.Error("main", "error", err)
		os.Exit(1)
	}
}

func Main() error {
	flagAddr := flag.String("listen", ":8080", "listening address")
	flag.Parse()
	http.Handle("/", nil)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return httpunix.ListenAndServe(ctx, *flagAddr, http.DefaultServeMux)
}
