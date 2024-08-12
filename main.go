// Copyright 2024 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nxadm/tail"
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
	root, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	http.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		io.WriteString(w, "<html></html>")
	})

	http.HandleFunc("/{file}", func(w http.ResponseWriter, r *http.Request) {
		fn, err := filepath.Abs(filepath.Join(root, r.PathValue("file")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		slog.Info("tail", "URL", r.URL, "method", r.Method)
		if !strings.HasPrefix(fn, root) {
			http.Error(w, fmt.Sprintf("only files under %q can be tailed", root), http.StatusBadRequest)
			return
		}
		tl, err := tail.TailFile(fn, tail.Config{ReOpen: true, MustExist: true, Follow: true, CompleteLines: true})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer tl.Cleanup()

		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, fmt.Sprintf("%T, not a http.Flusher", w), http.StatusInternalServerError)
			return
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ctx := r.Context()
		// Create a channel to send data
		for {
			select {
			case <-ctx.Done():
				return
			case line := <-tl.Lines:
				io.WriteString(w, "data: "+line.Text+"\n\n")
				fl.Flush()
			}
		}
	})

	slog.Info("Listen", "addr", *flagAddr, "root", root)
	return httpunix.ListenAndServe(ctx, *flagAddr, http.DefaultServeMux)
}
