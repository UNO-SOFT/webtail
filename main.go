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
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"github.com/tgulacsi/go/filterfs"
	"github.com/tgulacsi/go/httpunix"
	"github.com/tgulacsi/go/version"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	if err := Main(); err != nil {
		slog.Error("main", "error", err)
		os.Exit(1)
	}
}

func Main() error {
	appFS := ff.NewFlagSet("webtail")
	flagAddr := appFS.StringLong("listen", ":8080", "listening address")
	flagTLSCert := appFS.StringLong("tls-cert", os.ExpandEnv("$BRUNO_HOME/../admin/ssl/crt.pem"), "TLS Certificate PEM")
	flagTLSKey := appFS.StringLong("tls-key", os.ExpandEnv("$BRUNO_HOME/../admin/ssl/key.pem"), "TLS Key PEM")
	serve := func(ctx context.Context, hndl http.Handler) error {
		slog.Info("Listen", "addr", *flagAddr)
		if addr, ok := strings.CutPrefix(*flagAddr, "https://"); ok {
			return http.ListenAndServeTLS(addr, *flagTLSCert, *flagTLSKey, hndl)
		}
		return httpunix.ListenAndServe(ctx, *flagAddr, hndl)
	}

	FS := ff.NewFlagSet("serve")
	flagHMACKey := FS.String('k', "key", "", "base64 HMAC key")

	serveCmd := ff.Command{Name: "serve", Flags: FS,
		ShortHelp: "tail file, show on web",
		Usage:     "serve [opts] <log root>",
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
			if err := serveSetupHandlers(mux, root, macKey); err != nil {
				return err
			}
			return serve(ctx, mux)
		},
	}

	FS = ff.NewFlagSet("run")
	flagEmails := FS.StringListLong("email", "email addresses")
	flagGetenv := FS.StringLong("get-env", os.ExpandEnv(". ${BRUNO_HOME}/../.app_env"), "bash command to set up the environment")
	flagAsEphemeralService := FS.BoolLong("ephemeral-service", "start as ephemeral service")
	runCmd := ff.Command{Name: "run", Flags: FS,
		ShortHelp: "run program and tail output on web",
		Usage:     "run [opts] <program> [program args]",
		Exec: func(ctx context.Context, args []string) error {
			var prog string
			cmdArgs := make([]string, 0, 1+8+len(args))
			if !*flagAsEphemeralService {
				if *flagGetenv == "" {
					prog = args[0]
					cmdArgs = append(cmdArgs, args[1:]...)
				} else {
					var buf strings.Builder
					buf.WriteString(*flagGetenv)
					buf.WriteString(";")
					for _, a := range args {
						buf.WriteByte(' ')
						buf.WriteString(a)
					}
					prog = "/bin/bash"
					cmdArgs = append(cmdArgs, "-c", buf.String())
				}
			} else {
				prog = "systemd-run"
				argsAreUTF8 := utf8.Valid([]byte(args[0]))
				var buf bytes.Buffer
				for _, a := range args[1:] {
					if buf.Len() != 0 {
						buf.WriteByte(' ')
					}
					s, err := syntax.Quote(a, syntax.LangBash)
					if err != nil {
						return fmt.Errorf("quote %s: %w", a, err)
					}
					buf.WriteString(s)
					argsAreUTF8 = argsAreUTF8 && utf8.Valid([]byte(a))
				}
				hsh := sha256.Sum224(buf.Bytes())
				name := (base64.StdEncoding.EncodeToString([]byte(args[0])) +
					"-" + base64.StdEncoding.EncodeToString(hsh[:]))
				argsB64 := base64.StdEncoding.EncodeToString(buf.Bytes())
				var setup string
				if *flagGetenv != "" {
					setup = *flagGetenv + "; "
				}
				cmdArgs = append(cmdArgs, "systemd-run",
					"--user", "--collect", "--pipe",
					"--service-type=exec", "--unit="+name)
				if setup == "" && argsAreUTF8 {
					cmdArgs = append(cmdArgs, args...)
				} else {
					prog, err := syntax.Quote(args[0], syntax.LangBash)
					if err != nil {
						return fmt.Errorf("quote %s: %w", args[0], err)
					}
					cmdArgs = append(cmdArgs,
						"/bin/bash", "-c",
						fmt.Sprintf(
							"%sexec %s $(echo -n '%s' | base64 -d)",
							setup, prog, argsB64))
				}
			}

			cmd := exec.CommandContext(context.Background(), prog, cmdArgs...)
			slog.Info("start", "prog", cmd.Args)
			cmd.Stderr = cmd.Stderr
			out, err := cmd.StdoutPipe()
			if err != nil {
				return err
			}
			mux := http.DefaultServeMux
			done := make(chan error, 1)
			if err := runSetupHandlers(mux, out, done); err != nil {
				return err
			}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("start %q: %w", cmd.Args, err)
			}
			go func() {
				defer close(done)
				err := cmd.Wait()
				done <- err
				if len(*flagEmails) == 0 {
					return
				}
				var typ string
				var rc int
				if err == nil {
					typ = "sikeresen"
				} else {
					typ = "HIBAval"
					var ee *exec.ExitError
					if errors.As(err, &ee) {
						rc = ee.ExitCode()
					}
				}
				mail := exec.CommandContext(ctx, "mail",
					append(append(make([]string, 0, 2+len(*flagEmails)),
						"-s", strings.Join(args, " ")+" "+typ+" lefutott"),
						*flagEmails...)...)
				mail.Stdin = strings.NewReader(
					strings.Join(cmd.Args, " ") + ": " + strconv.Itoa(rc))
				mail.Stdout, mail.Stderr = os.Stdout, os.Stderr
				if err := mail.Start(); err != nil {
					slog.Error("sending mail", "cmd", mail.Args, "error", err)
				}
			}()
			return serve(ctx, mux)
		},
	}

	flagVersion := appFS.Bool('V', "version", "print version and exit")
	app := ff.Command{Name: "webtail", Flags: appFS,
		Subcommands: []*ff.Command{&serveCmd, &runCmd},
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

func serveSetupHandlers(mux *http.ServeMux, rootPath string, macKey []byte) error {
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

        <script src="https://cdn.jsdelivr.net/npm/htmx.org@2.0.8/dist/htmx.min.js" integrity="sha384-/TgkGk7p307TH7EXJDuUlgG3Ce1UVolAOFopFekQkkXihi5u/6OCvVKyz1W+idaz" crossorigin="anonymous"></script>
        <script src="https://cdn.jsdelivr.net/npm/htmx-ext-sse@2.2.4" integrity="sha384-A986SAtodyH8eg8x8irJnYUk7i9inVQqYigD6qZ9evobksGNIXfeFvDwLSHcp31N" crossorigin="anonymous"></script>
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
