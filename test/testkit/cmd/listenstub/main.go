// listenstub is a fake tunnel CLI (aws, gcloud, cloud-sql-proxy) for tests:
// it appends its arguments to $STUB_LOG, then listens on the local port
// found in them — as a real tunnel would — until SIGTERM (exit 143).
// STUB_NOLISTEN=1 makes it run without listening (a tunnel that never gets
// ready); STUB_EXIT=n makes it exit n at once.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The local port, as each tunnel CLI takes it.
var portArg = regexp.MustCompile(`"localPortNumber":\["(\d+)"\]|--local-host-port=localhost:(\d+)|--port (\d+)`)

func main() {
	args := strings.Join(os.Args[1:], " ")
	if log := os.Getenv("STUB_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s %s\n", filepath.Base(os.Args[0]), args)
			f.Close()
		}
	}
	if code := os.Getenv("STUB_EXIT"); code != "" {
		fmt.Println("listenstub: exiting", code)
		n, _ := strconv.Atoi(code)
		os.Exit(n)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	if os.Getenv("STUB_NOLISTEN") == "" {
		m := portArg.FindStringSubmatch(args)
		if m == nil {
			fmt.Fprintln(os.Stderr, "listenstub: no local port in", args)
			os.Exit(2)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+m[1]+m[2]+m[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "listenstub:", err)
			os.Exit(2)
		}
		fmt.Println("listenstub: listening on", ln.Addr())
		go func() {
			for {
				if c, err := ln.Accept(); err == nil {
					c.Close()
				}
			}
		}()
	}
	select {
	case <-sigs:
		fmt.Println("listenstub: stopped")
		os.Exit(143)
	case <-time.After(10 * time.Minute):
		os.Exit(1)
	}
}
