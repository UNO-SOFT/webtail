// Copyright 2024, 2026 Tamás Gulácsi. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	crand "crypto/rand"
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

	"github.com/go-json-experiment/json"
	"github.com/godror/godror/cloexec"
	"github.com/oklog/ulid/v2"
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
	// slog.Info("finish")
}

func Main() error {
	appFS := ff.NewFlagSet("webtail")
	flagAddr := appFS.StringLong("listen", ":8080", "listening address")
	flagTLSCert := appFS.StringLong("tls-cert", os.ExpandEnv("$BRUNO_HOME/../admin/ssl/crt.pem"), "TLS Certificate PEM")
	flagTLSKey := appFS.StringLong("tls-key", os.ExpandEnv("$BRUNO_HOME/../admin/ssl/key.pem"), "TLS Key PEM")
	if strings.HasPrefix(*flagAddr, ":") {
		if fi, err := os.Stat(*flagTLSCert); err == nil && fi.Size() != 0 {
			if fi, err := os.Stat(*flagTLSKey); err == nil && fi.Size() != 0 {
				if hostname, _ := os.Hostname(); hostname != "" {
					*flagAddr = "https://" + hostname + *flagAddr
				}
			}
		}
	}
	serve := func(ctx context.Context, addr string, hndl http.Handler) error {
		slog.Info("Listen", "addr", addr)
		if addr, ok := strings.CutPrefix(addr, "https://"); ok {
			return http.ListenAndServeTLS(addr, *flagTLSCert, *flagTLSKey, hndl)
		}
		return httpunix.ListenAndServe(ctx, addr, hndl)
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
			if err := setupHandlers(mux, root, macKey); err != nil {
				return err
			}
			return serve(ctx, *flagAddr, mux)
		},
	}

	// Ez sem jó: nem bruno alatt kéne futnia, hogy ne álljon le a szervízek leállításakor.
	// De akkor nem ugyanaz a környezet, user stb.
	FS = ff.NewFlagSet("service")
	serviceCmd := ff.Command{Name: "service", Flags: FS,
		Exec: func(ctx context.Context, args []string) error {
			http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				args := append(append(make([]string, 0, 1+len(q["args"])), q.Get("program")), q["args"]...)
				var setup string
				var sleep time.Duration
				{
					var buf strings.Builder
					if getenv := q.Get("getenv"); getenv != "" {
						buf.WriteString(getenv)
						buf.WriteString("; ")
					}
					setup = buf.String()
				}
				if s := q.Get("sleep"); s != "" {
					var err error
					if sleep, err = time.ParseDuration(s); err != nil {
						http.Error(w, fmt.Sprintf("parse sleep=%s: %+v", s, err), http.StatusBadRequest)
						return
					}
				}
				argsAreUTF8 := utf8.Valid([]byte(args[0]))
				var buf bytes.Buffer
				for _, a := range args[1:] {
					if buf.Len() != 0 {
						buf.WriteByte(' ')
					}
					s, err := syntax.Quote(a, syntax.LangBash)
					if err != nil {
						http.Error(w, fmt.Sprintf("quote %s: %w", a, err), http.StatusBadRequest)
						return
					}
					buf.WriteString(s)
					argsAreUTF8 = argsAreUTF8 && utf8.Valid([]byte(a))
				}
				name := ulid.Make().String()
				argsB64 := base64.StdEncoding.EncodeToString(buf.Bytes())

				prog := "systemd-run"
				cmdArgs := append(make([]string, 0, 4+len(args)),
					"--user", "--collect",
					"--service-type=exec", "--unit=webtail-"+name)
				if sleep != 0 {
					cmdArgs = append(cmdArgs, "--timer-property=AccuracySec=1s",
						"--on-active="+sleep.String())
				}
				if setup == "" && argsAreUTF8 {
					cmdArgs = append(cmdArgs, args...)
				} else {
					prg, err := syntax.Quote(args[0], syntax.LangBash)
					if err != nil {
						http.Error(w, fmt.Sprintf("quote %s: %w", args[0], err), http.StatusBadRequest)
						return
					}
					cmdArgs = append(cmdArgs,
						"/bin/bash", "-c",
						setup+fmt.Sprintf(
							"exec %s $(echo -n '%s' | base64 -d)",
							prg, argsB64))
				}
				cmd := exec.CommandContext(context.Background(), prog, cmdArgs...)
				if err := cloexec.SetNetConnections("tcp"); err != nil {
					slog.Warn("cloexec.SetNetConnections", "error", err)
				}
				if err := cmd.Start(); err != nil {
					http.Error(w, fmt.Sprintf("start %s: %w", cmd.Args, err), http.StatusInternalServerError)
					return
				}
				io.WriteString(w, "/follow?name="+name)
			})
			return serve(ctx, *flagAddr, http.DefaultServeMux)
		},
	}

	type Parameters struct {
		Args                  [][]byte `json:"format:base64"`
		MacKey                []byte   `json:"format:hex"`
		Emails                []string
		Listen                string
		Name, Getenv, LogFile string
		// Sleep                 time.Duration `json:",format:sec"`
	}

	runCmd := ff.Command{Name: "run",
		ShortHelp: "run program and tail output on web",
		Usage:     "run {Parameters json base64} or {json in stdin}",
		Exec: func(ctx context.Context, args []string) error {
			var params Parameters
			if len(args) == 0 || args[0] == "" || args[0] == "-" {
				if err := json.UnmarshalRead(os.Stdin, &params); err != nil {
					return err
				}
			} else {
				var buf bytes.Buffer
				for _, a := range args {
					b, err := base64.StdEncoding.DecodeString(a)
					if err != nil {
						return err
					}
					buf.Write(b)
				}
				if err := json.Unmarshal(buf.Bytes(), &params); err != nil {
					return err
				}
			}
			// slog.Info("got", "params", params)
			var setup string
			{
				var buf strings.Builder
				if params.Getenv != "" {
					buf.WriteString(params.Getenv)
					buf.WriteString("; ")
				}
				setup = buf.String()
			}

			argsAreUTF8 := utf8.Valid(params.Args[0])
			args = make([]string, 0, len(params.Args))
			for i, a := range params.Args {
				if i == 0 {
					args = append(args, string(a))
					continue
				}
				if !utf8.Valid(a) {
					args = append(args, `"$(echo `+base64.StdEncoding.EncodeToString(a)+` | base64 -d)"`)
					argsAreUTF8 = false
					continue
				}
				s := string(a)
				if q, err := syntax.Quote(s, syntax.LangBash); err != nil {
					return fmt.Errorf("quote %s: %w", s, err)
				} else {
					s = q
				}
				args = append(args, s)
			}
			slog.Debug("go", "args", fmt.Sprintf("%q", args))

			cmdArgs := append(make([]string, 0, 1+11+len(args)), "systemd-run",
				"--user", "--collect", "-p", "StandardError=inherit",
				"--service-type=exec", "--unit="+params.Name)
			if params.LogFile == "" {
				cmdArgs = append(cmdArgs,
					"--pipe",
					"-p", "StandardOutput=journal",
				)
			} else {
				cmdArgs = append(cmdArgs, "-p", "StandardOutput=append:"+params.LogFile)
			}
			if setup == "" && argsAreUTF8 {
				cmdArgs = append(cmdArgs, args...)
			} else {
				cmdArgs = append(cmdArgs,
					"/bin/bash", "-c", setup+strings.Join(args, " "))
			}

			mux := http.DefaultServeMux
			slog.Debug("setupHandlers", "file", params.LogFile)
			if err := setupHandlers(mux, filepath.Dir(params.LogFile), params.MacKey); err != nil {
				return err
			}

			cmd := exec.CommandContext(context.Background(), cmdArgs[0], cmdArgs[1:]...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			slog.Info("start", "prog", fmt.Sprintf("%q", cmd.Args))
			if err := cloexec.SetNetConnections("tcp"); err != nil {
				slog.Warn("cloexec.SetNetConnections", "error", err)
			}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("start %q: %w", cmd.Args, err)
			}

			done := make(chan error, 1)
			go func() {
				defer close(done)
				err := cmd.Wait()
				done <- err
				slog.Warn("finished", "error", err)
				if len(params.Emails) == 0 {
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
					append(append(make([]string, 0, 2+len(params.Emails)),
						"-s", strings.Join(args, " ")+" "+typ+" lefutott"),
						params.Emails...)...)
				mail.Stdin = strings.NewReader(
					strings.Join(cmd.Args, " ") + ": " + strconv.Itoa(rc))
				mail.Stdout, mail.Stderr = os.Stdout, os.Stderr
				if err := mail.Start(); err != nil {
					slog.Error("sending mail", "cmd", mail.Args, "error", err)
				}
			}()
			return serve(ctx, params.Listen, mux)
		},
	}

	FS = ff.NewFlagSet("start")
	flagLogFile := FS.StringLong("log-file", os.ExpandEnv("$BRUNO_HOME/data/mai/log/webtail-{{.Name}}.log"), "log file")
	flagEmails := FS.StringListLong("email", "email addresses")
	flagGetenv := FS.StringLong("get-env", os.ExpandEnv(". ${BRUNO_HOME}/../.app_env"), "bash command to set up the environment")
	flagSleep := FS.DurationLong("sleep", 0, "sleep before starting command")
	flagWait := FS.BoolLong("wait", "start and wait the program to finish")
	startCmd := ff.Command{Name: "start", Flags: FS,
		ShortHelp: "start  program and tail output on web",
		Usage:     "start [opts] <program> [program args]",
		Exec: func(ctx context.Context, args []string) error {
			if *flagLogFile == "" {
				*flagLogFile = os.ExpandEnv("$BRUNO_HOME/data/mai/log/webtail-{{.Name}}.log")
			}
			var a [8]byte
			n, _ := crand.Read(a[:])
			params := Parameters{
				Name:   ulid.Make().String(),
				Listen: *flagAddr,
				Emails: *flagEmails, Getenv: *flagGetenv,
				MacKey: a[:n], Args: make([][]byte, len(args)),
			}
			params.LogFile = strings.ReplaceAll(*flagLogFile, "{{.Name}}", params.Name)
			for i, a := range args {
				params.Args[i] = []byte(a)
			}
			hsh := hmac.New(sha256.New, params.MacKey)
			bn := filepath.Base(params.LogFile)
			io.WriteString(hsh, bn)
			want := base64.URLEncoding.EncodeToString(hsh.Sum(nil))
			addr := *flagAddr
			if strings.HasPrefix(addr, ":") {
				addr = "http://localhost" + addr
			}
			fmt.Println(addr + "/tail?left=&right=%3Cbr%3E&file=" + url.PathEscape(bn) + "&mac=" + want)

			self, err := os.Executable()
			if err != nil {
				return err
			}
			b, err := json.Marshal(params)
			if err != nil {
				return err
			}
			if err := cloexec.SetNetConnections("tcp"); err != nil {
				slog.Warn("cloexec.SetNetConnections", "error", err)
			}
			if *flagWait {
				return runCmd.Exec(ctx, []string{base64.StdEncoding.EncodeToString(b)})
			}

			cmdArgs := append(make([]string, 0, 11),
				"--user", "--collect", "--no-block",
				"--service-type=exec", "--unit=webtail-start-"+params.Name,
				"-p", "StandardInputData="+base64.StdEncoding.EncodeToString(b),
			)
			if *flagSleep != 0 {
				cmdArgs = append(cmdArgs,
					"--timer-property=AccuracySec=1s",
					"--on-active="+flagSleep.String())
			}
			cmd := exec.CommandContext(context.Background(),
				"systemd-run", append(cmdArgs,
					self, "run",
				)...)
			slog.Info("send", "params", string(b), "call", cmd.Args)
			return cmd.Run()
		},
	}

	flagVersion := appFS.Bool('V', "version", "print version and exit")
	app := ff.Command{Name: "webtail", Flags: appFS,
		Subcommands: []*ff.Command{&serveCmd, &startCmd, &runCmd, &serviceCmd},
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
		rootPath = filepath.Dir(rootPath)
		slog.Info("stat non dir", "file", fi.Name(), "root", rootPath)
		filterFS = func(fsys fs.FS) fs.FS {
			return filterfs.NewOneFileFS(fsys, filepath.Base(fi.Name()))
		}
	}
	slog.Debug("open", "root", rootPath)
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
		if fi, err := fs.Stat(FS, p); err != nil {
			slog.Error("stat", "path", p, "root", root, "error", err)
			p = "/"
		} else if !fi.Mode().IsDir() {
			slog.Error("mode", "path", p, "mode", fi.Mode())
			p = path.Dir(p)
		}

		slog.Info("/", "path", p)
		dis, err := fs.ReadDir(FS, p)
		if len(dis) == 0 {
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			slog.Warn("empty", "path", p)
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
		if fi, err := fs.Stat(FS, fn); err != nil {
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
		if fi, err := fs.Stat(FS, fn); err != nil {
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
