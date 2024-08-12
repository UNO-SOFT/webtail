// Copyright 2024 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		dis, err := os.ReadDir(root)
		if len(dis) == 0 && err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		io.WriteString(w, `<!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>
    </head>
<body>
<p>
<ul>
`)
		for _, di := range dis {
			if di.IsDir() || !di.Type().IsRegular() {
				continue
			}
			fmt.Fprintf(w, "<li><a href=\"./tail?file="+url.PathEscape(di.Name())+"\">"+html.EscapeString(di.Name())+"</a></li>\n")
		}
		io.WriteString(w, `
	</ul></p>
</body>
</html>`)
	})
	http.HandleFunc("GET /tail", func(w http.ResponseWriter, r *http.Request) {
		fn := r.URL.Query().Get("file")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		io.WriteString(w, `<!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>

        <script src="https://unpkg.com/htmx.org@2.0.1" integrity="sha384-QWGpdj554B4ETpJJC9z+ZHJcA/i59TyjxEPXiiUgN2WmTyV5OEZWCD6gQhgkdpB/" crossorigin="anonymous"></script>
        <script src="https://unpkg.com/htmx-ext-sse@2.2.1/sse.js"></script>
    </head>
    <body>
        <h1>`+html.EscapeString(fn)+`</h1>
        <div hx-ext="sse" sse-connect="/tail-sse?wrap=pre&file=`+
			url.QueryEscape(fn)+
			`" sse-swap="message" hx-swap="afterend swap:1s">
        </div>
    </body>
</html>`)
	})

	http.HandleFunc("/tail-sse", func(w http.ResponseWriter, r *http.Request) {
		wrap := r.URL.Query().Get("wrap")
		fn, err := filepath.Abs(filepath.Join(root, r.URL.Query().Get("file")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		slog.Info("tail", "URL", r.URL, "method", r.Method)
		if !strings.HasPrefix(fn, root) {
			http.Error(w, fmt.Sprintf("only files under %q can be tailed", root), http.StatusBadRequest)
			return
		}
		tl, err := tail.TailFile(fn, tail.Config{
			ReOpen: true, Follow: true,
			MustExist:     true,
			Poll:          true,
			CompleteLines: true,
		})
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
				if wrap != "" {
					io.WriteString(w, "data: <"+wrap+">"+html.EscapeString(line.Text)+"</"+wrap+">\n\n")
				} else {
					io.WriteString(w, "data: "+line.Text+"\n\n")
				}
				fl.Flush()
			}
		}
	})

	slog.Info("Listen", "addr", *flagAddr, "root", root)
	return httpunix.ListenAndServe(ctx, *flagAddr, http.DefaultServeMux)
}
