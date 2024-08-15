// Copyright 2024 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
			var prefix string
			if di.Type().IsDir() {
				prefix = "dir"
			} else if di.Type().IsRegular() {
				prefix = "file"
			} else {
				continue
			}
			io.WriteString(w, "<li><a href=\"./"+prefix+"?path="+url.PathEscape(afn)+"\">"+html.EscapeString(bn)+"</a></li>\n")
		}
		io.WriteString(w, `
	</ul></p>
</body>
</html>`)
	})

	http.HandleFunc("GET /file", func(w http.ResponseWriter, r *http.Request) {
		fn := path.Clean(r.URL.Query().Get("path"))
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
        <pre hx-ext="sse" sse-connect="/tail?left=&right=`+
			url.QueryEscape(`<br>`)+
			`&file=`+
			url.QueryEscape(fn)+
			`" sse-swap="message" hx-swap="beforebegin swap:1s">
        </pre>
    </body>
</html>`)
	})

	http.HandleFunc("/tail", func(w http.ResponseWriter, r *http.Request) {
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
		fh, err := os.Open(afn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer fh.Close()
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
		linesCh := make(chan string)
		go tailFile(ctx, linesCh, fh)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		bw := bufio.NewWriter(w)
		// Create a channel to send data
		for {
			select {
			case <-ctx.Done():
				return

			case line, ok := <-linesCh:
				if !ok {
					bw.Flush()
					fl.Flush()
					return
				}
				bw.WriteString("data: ")
				if left == "" && right == "" {
					bw.WriteString(line)
				} else {
					bw.WriteString(left)
					bw.WriteString(html.EscapeString(line))
					bw.WriteString(right)
				}
				bw.WriteString("\n\n")

			case <-ticker.C:
				if bw.Buffered() != 0 {
					bw.Flush()
					fl.Flush()
				}
			}
		}
	})

	slog.Info("Listen", "addr", *flagAddr, "root", root)
	return httpunix.ListenAndServe(ctx, *flagAddr, http.DefaultServeMux)
}

func tailFile(ctx context.Context, linesCh chan<- string, fh *os.File) error {
	defer func() {
		slog.Info("finish", "tail", fh.Name())
		fh.Close()
		close(linesCh)
	}()
	var off int64
	var a [16384]byte
	var start int
	dur := time.Second
	timer := time.NewTimer(dur)
	for {
		n, err := fh.ReadAt(a[start:], off)
		slog.Info("ReadAt", "off", off, "start", start, "n", n, "error", err)
		if n == 0 {
			dur += time.Duration(float32(time.Second) * rand.Float32())
			timer.Reset(dur)
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil
			}
			continue
		}
		dur = time.Second
		off += int64(n)
		p := a[:start+n]
		for {
			if i := bytes.IndexByte(p, '\n'); i < 0 {
				start = copy(a[0:], p)
				break
			} else {
				select {
				case <-ctx.Done():
					return nil
				case linesCh <- string(p[:i]):
					p = p[i+1:]
				}
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
}
