package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackall3n/run-stack/internal/certs"
	"github.com/jackall3n/run-stack/internal/state"
)

type recorder struct{ hosts []string }

func (recorder *recorder) Set(hosts []string) {
	recorder.hosts = append([]string{}, hosts...)
	sort.Strings(recorder.hosts)
}

func (recorder *recorder) Close() {}

func TestRoutesByHostOverTrustedTLS(t *testing.T) {
	t.Setenv("RUN_STACK_HOME", t.TempDir())

	var seenHost, seenProto string

	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenHost, seenProto = request.Host, request.Header.Get("X-Forwarded-Proto")
		io.WriteString(writer, "hello from the stack")
	}))
	defer backend.Close()

	_, portText, _ := net.SplitHostPort(backend.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	err := state.Update(func(current *state.State) error {
		current.Stacks["shop"] = &state.Entry{
			ID:    "shop",
			PID:   os.Getpid(),
			Ports: map[string]int{"WEB_PORT": port, "EXPLORER_PORT": port},
			Routes: []state.Route{
				{Host: "shop.local", Port: port, Stack: "shop", Name: "WEB_PORT"},
				{Host: "explorer.shop.local", Port: port, HostHeader: "localhost", Stack: "shop", Name: "EXPLORER_PORT"},
			},
		}
		// Not running, so neither routed nor announced.
		current.Stacks["old"] = &state.Entry{ID: "old", Routes: []state.Route{{Host: "old.local", Port: port}}}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	authority, err := certs.Load()
	if err != nil {
		t.Fatal(err)
	}

	announced := &recorder{}
	server := httptest.NewUnstartedServer(newRouter(443, announced))
	server.TLS = &tls.Config{GetCertificate: authority.Certificate}
	server.StartTLS()
	defer server.Close()

	if got := strings.Join(announced.hosts, ","); got != "explorer.shop.local,shop.local" {
		t.Errorf("announced %s", got)
	}

	roots := x509.NewCertPool()
	roots.AddCert(authority.Root)

	call := func(method, host, path string, headers map[string]string) (int, string) {
		t.Helper()

		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host}}}

		request, _ := http.NewRequest(method, server.URL+path, nil)
		request.Host = host

		for key, value := range headers {
			request.Header.Set(key, value)
		}

		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		defer response.Body.Close()

		body, _ := io.ReadAll(response.Body)

		return response.StatusCode, string(body)
	}

	if status, body := call("GET", "shop.local", "/", nil); status != 200 || body != "hello from the stack" {
		t.Errorf("routed host: %d %q", status, body)
	}

	if seenHost != "shop.local" || seenProto != "https" {
		t.Errorf("upstream saw host %q proto %q", seenHost, seenProto)
	}

	if call("GET", "explorer.shop.local", "/", nil); seenHost != "localhost" {
		t.Errorf("host_header not applied: upstream saw %q", seenHost)
	}

	if status, body := call("GET", "old.local", "/", nil); status != 404 || !strings.Contains(body, "https://shop.local") {
		t.Errorf("stopped stack's host: %d %q", status, body)
	}
}
