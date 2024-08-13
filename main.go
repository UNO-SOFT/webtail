// Copyright 2024 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
	FS := os.DirFS(root)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Query().Get("path"))
		if fi, err := FS.(fs.StatFS).Stat(p); err != nil {
			slog.Error("stat", "path", p, "root", root, "error", err)
			p = "/"
		} else if !fi.Mode().IsDir() {
			slog.Error("mode", "path", p, "mode", fi.Mode())
			p = path.Dir(p)
		}

		slog.Info("/", "path", p)
		dis, err := FS.(fs.ReadDirFS).ReadDir(p)
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
			bn := di.Name()
			afn := path.Join(p, bn)
			if di.Type().IsDir() {
				io.WriteString(w, "<li><a href=\"./?path="+url.PathEscape(afn)+"\">"+html.EscapeString(bn)+"</a></li>\n")
			} else if !di.Type().IsRegular() {
				continue
			} else {
				io.WriteString(w, "<li><a href=\"./tail?file="+url.PathEscape(afn)+"\">"+html.EscapeString(bn)+"</a></li>\n")
			}
		}
		io.WriteString(w, `
	</ul></p>
</body>
</html>`)
	})

	http.HandleFunc("GET /tail", func(w http.ResponseWriter, r *http.Request) {
		fn := path.Clean(r.URL.Query().Get("file"))
		if fi, err := FS.(fs.StatFS).Stat(fn); err != nil {
			slog.Error("stat", "file", fn, "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		} else if !fi.Mode().IsRegular() {
			slog.Error("not regular", "file", fn, "mode", fi.Mode())
			http.Error(w, fmt.Sprintf("%q is not a regular file (%v)", fn, fi), http.StatusBadRequest)
			return
		}

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
        <pre hx-ext="sse" sse-connect="/tail-sse?left=&right=<br>&file=`+
			url.QueryEscape(fn)+
			`" sse-swap="message" hx-swap="afterbegin swap:1s">
        </pre>
    </body>
</html>`)
	})

	http.HandleFunc("/tail-sse", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		left := q.Get("left")
		right := q.Get("right")
		fn := path.Clean(q.Get("file"))
		if fi, err := FS.(fs.StatFS).Stat(fn); err != nil {
			slog.Error("stat", "file", fn, "root", root, "error", err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		} else if !fi.Mode().IsRegular() {
			slog.Error("not regular", "file", fn, "root", root, "mode", fi.Mode())
			http.Error(w, fmt.Sprintf("%q is not a regular file (%v)", fn, fi.Mode()), http.StatusBadRequest)
			return
		}

		slog.Info("tail", "URL", r.URL, "method", r.Method, "file", fn)
		afn, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(fn)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(afn, root) {
			http.Error(w, fmt.Sprintf("only files under %q can be tailed (%q)", root, afn), http.StatusBadRequest)
			return
		}
		tl, err := tail.TailFile(afn, tail.Config{
			ReOpen: true, Follow: true,
			MustExist:     true,
			Poll:          true, // otherwise close/reopen fails
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

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		bw := bufio.NewWriter(w)
		ctx := r.Context()
		// Create a channel to send data
		for {
			select {
			case <-ctx.Done():
				return

			case line := <-tl.Lines:
				bw.WriteString("data: ")
				if left == "" && right == "" {
					bw.WriteString(line.Text)
				} else {
					bw.WriteString(left)
					bw.WriteString(html.EscapeString(line.Text))
					bw.WriteString(right)
				}
				bw.WriteString("\n\n")

			case <-ticker.C:
				bw.Flush()
				fl.Flush()
			}
		}
	})

	slog.Info("Listen", "addr", *flagAddr, "root", root)
	return httpunix.ListenAndServe(ctx, *flagAddr, http.DefaultServeMux)
}
