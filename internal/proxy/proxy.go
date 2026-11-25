// Package proxy is the one shared process that fronts every stack: it serves HTTPS for each
// routed host, and makes .local names resolve.
package proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackall3n/run-stack/internal/certs"
	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/dns"
	"github.com/jackall3n/run-stack/internal/home"
)

// PID is the running proxy's process id, or 0.
func PID() int {
	data, _ := os.ReadFile(home.ProxyPID())
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))

	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}

	return pid
}

// loopbackOnly drops connections from other machines. The proxy has to listen on every address
// (macOS only lets an ordinary user bind ports below 1024 that way), but what it serves is dev
// servers and a page that can start and stop them, so nothing off this machine gets through.
type loopbackOnly struct{ net.Listener }

func (listener loopbackOnly) Accept() (net.Conn, error) {
	for {
		conn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}

		if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok && addr.IP.IsLoopback() {
			return conn, nil
		}

		conn.Close()
	}
}

// Serve is the proxy itself: it runs until killed. HTTPS on one port routes to the stacks, HTTP
// on the other redirects to it.
func Serve() error {
	settings := home.LoadSettings()

	authority, err := certs.Load()
	if err != nil {
		return err
	}

	secure, err := net.Listen("tcp", ":"+strconv.Itoa(settings.HTTPSPort))
	if err != nil {
		return err
	}

	plain, err := net.Listen("tcp", ":"+strconv.Itoa(settings.HTTPPort))
	if err != nil {
		return err
	}

	announcer := dns.New()
	router := newRouter(settings.HTTPSPort, announcer)

	if err := os.WriteFile(home.ProxyPID(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-signals
		announcer.Close()
		os.Remove(home.ProxyPID())
		os.Exit(0)
	}()

	// Stacks come and go by changing the state file; this is what notices.
	go func() {
		for range time.Tick(300 * time.Millisecond) {
			router.refresh()
		}
	}()

	go http.Serve(loopbackOnly{plain}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, config.Origin(hostOf(request), settings.HTTPSPort)+request.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))

	fmt.Printf("proxy listening on :%d and :%d\n", settings.HTTPSPort, settings.HTTPPort)

	server := &http.Server{Handler: router, TLSConfig: &tls.Config{GetCertificate: authority.Certificate}}

	return server.ServeTLS(loopbackOnly{secure}, "", "")
}

// Ensure starts the proxy in the background unless one is already running.
func Ensure() error {
	if PID() != 0 {
		return nil
	}

	if err := os.MkdirAll(home.LogDir(), 0o755); err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}

	log, err := os.OpenFile(home.ProxyLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()

	cmd := exec.Command(self, "proxy", "run")
	cmd.Stdout, cmd.Stderr = log, log
	// Its own session, so it outlives the terminal and the stack that happened to start it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	exited := make(chan struct{})

	go func() {
		cmd.Wait()
		close(exited)
	}()

	for attempt := 0; attempt < 100; attempt++ {
		if PID() != 0 {
			return nil
		}

		select {
		case <-exited:
			// Another run-stack may have won the race to start it; that is fine.
			if PID() != 0 {
				return nil
			}

			reason, _ := os.ReadFile(home.ProxyLog())
			settings := home.LoadSettings()

			return fmt.Errorf("the proxy could not start on ports %d and %d: %s", settings.HTTPSPort, settings.HTTPPort, strings.TrimSpace(string(reason)))
		case <-time.After(50 * time.Millisecond):
		}
	}

	return fmt.Errorf("the proxy did not start. See %s", home.ProxyLog())
}

// Stop ends the proxy. Running stacks keep running, but their URLs stop answering until it is back.
func Stop() bool {
	pid := PID()

	if pid == 0 {
		return false
	}

	syscall.Kill(pid, syscall.SIGTERM)

	for waited := 0; waited < 30 && PID() != 0; waited++ {
		time.Sleep(50 * time.Millisecond)
	}

	os.Remove(home.ProxyPID())

	return true
}
