// Copyright 2024, 2025 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
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
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"github.com/tgulacsi/go/filterfs"
	"github.com/tgulacsi/go/httpunix"
	"github.com/tgulacsi/go/version"
)

func main() {
	if err := Main(); err != nil {
		slog.Error("main", "error", err)
		os.Exit(1)
	}
}

func Main() error {
	FS := ff.NewFlagSet("webtail")
	flagAddr := FS.StringLong("listen", ":8080", "listening address")
	flagVersion := FS.Bool('V', "version", "print version and exit")
	flagHMACKey := FS.String('k', "key", "", "base64 HMAC key")
	app := ff.Command{Name: "webtail", Flags: FS,
		ShortHelp: "tail file, show on web",
		Usage:     "webtail [opts] <log root>",
		Exec: func(ctx context.Context, args []string) error {
			root, err := os.Getwd()
			if len(args) != 0 {
				root, err = filepath.Abs(args[0])
			}
			if err != nil {
				return err
			}
			var macKey []byte
			if *flagHMACKey != "" {
				if macKey, err = base64.StdEncoding.DecodeString(*flagHMACKey); err != nil {
					return err
				}
			}

			mux := http.DefaultServeMux
			if err := setupHandlers(mux, root, macKey); err != nil {
				return err
			}
			slog.Info("Listen", "addr", *flagAddr, "root", root)
			return httpunix.ListenAndServe(ctx, *flagAddr, mux)
		},
	}

	if err := app.Parse(os.Args[1:]); err != nil {
		ffhelp.Command(&app).WriteTo(os.Stderr)
		if errors.Is(err, ff.ErrHelp) {
			return nil
		}
		return err
	} else if *flagVersion {
		fmt.Println(version.Main())
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return app.Run(ctx)
}

func setupHandlers(mux *http.ServeMux, rootPath string, macKey []byte) error {
	if len(macKey) == 0 {
		slog.Warn("Empty macKey")
	}
	var filterFS func(fs.FS) fs.FS
	if fi, err := os.Stat(rootPath); err == nil && !fi.IsDir() {
		rootPath = filepath.Dir(fi.Name())
		filterFS = func(fsys fs.FS) fs.FS {
			return filterfs.NewOneFileFS(fsys, filepath.Base(fi.Name()))
		}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("openRoot: %w", err)
	}
	FS := root.FS()
	if filterFS != nil {
		FS = filterFS(FS)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
		hsh := hmac.New(sha256.New, macKey)
		var b []byte
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
			hsh.Reset()
			io.WriteString(hsh, afn)
			b = hsh.Sum(b[:0])
			io.WriteString(w, "<li><a href=\"./"+prefix+"?path="+url.PathEscape(afn)+"&mac="+base64.URLEncoding.EncodeToString(b)+"\">"+html.EscapeString(bn)+"</a></li>\n")
		}
		io.WriteString(w, `
	</ul></p>
</body>
</html>`)
	})

	mux.HandleFunc("GET /file", func(w http.ResponseWriter, r *http.Request) {
		fn := path.Clean(r.URL.Query().Get("path"))
		got := r.URL.Query().Get("mac")
		if err := checkMac(macKey, fn, got); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
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
			"&mac="+url.QueryEscape(got)+
			`" sse-swap="message" hx-swap="beforebegin swap:1s">
        </pre>
    </body>
</html>`)
	})

	mux.HandleFunc("/tail", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		left := q.Get("left")
		right := q.Get("right")
		fn := path.Clean(q.Get("file"))
		if err := checkMac(macKey, fn, q.Get("mac")); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
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
		fh, err := root.Open(fn)
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

	return nil
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

var errHashMismatch = errors.New("hash mismatch")

func checkMac(macKey []byte, s, got string) error {
	if len(macKey) == 0 {
		slog.Warn("empty macKey")
		return nil
	}
	hsh := hmac.New(sha256.New, macKey)
	io.WriteString(hsh, s)
	want, err := base64.URLEncoding.DecodeString(got)
	if err != nil {
		slog.Error("decode", "base64", got, "error", err)
		return fmt.Errorf("decode mac: %w", err)
	}
	if !hmac.Equal(hsh.Sum(nil), want) {
		slog.Error("hash mismatch", "want", want, "got", got)
		return errHashMismatch
	}
	return nil
}
