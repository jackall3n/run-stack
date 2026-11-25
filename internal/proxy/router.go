package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/dns"
	"github.com/jackall3n/run-stack/internal/state"
)

// router sends each request to the port its host is mapped to. The mapping is the state file's
// running stacks, reread a few times a second, so stacks come and go without the proxy being told.
type router struct {
	httpsPort int
	transport http.RoundTripper
	announcer dns.Announcer

	mu     sync.RWMutex
	routes map[string]state.Route
}

func newRouter(httpsPort int, announcer dns.Announcer) *router {
	router := &router{
		httpsPort: httpsPort,
		announcer: announcer,
		transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	router.refresh()

	return router
}

// refresh rereads which hosts are live, and keeps exactly those (and the dashboard) resolving.
func (router *router) refresh() {
	routes := map[string]state.Route{}
	hosts := []string{}

	for host := range config.DashboardHosts {
		hosts = append(hosts, host)
	}

	for _, entry := range state.Read().Stacks {
		if entry.Running() {
			for _, route := range entry.Routes {
				routes[route.Host] = route
				hosts = append(hosts, route.Host)
			}
		}
	}

	router.mu.Lock()
	router.routes = routes
	router.mu.Unlock()

	router.announcer.Set(hosts)
}

func (router *router) lookup(host string) (state.Route, bool) {
	router.mu.RLock()
	route, ok := router.routes[host]
	router.mu.RUnlock()

	return route, ok
}

func hostOf(request *http.Request) string {
	host := strings.ToLower(request.Host)

	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}

	return host
}

func (router *router) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	host := hostOf(request)

	if config.DashboardHosts[host] {
		router.dashboard(writer, request)

		return
	}

	route, ok := router.lookup(host)

	if !ok {
		// A stack that started a moment ago may not have been noticed yet.
		router.refresh()
		route, ok = router.lookup(host)
	}

	if !ok {
		router.notFound(writer, host)

		return
	}

	proxy := &httputil.ReverseProxy{
		Transport: router.transport,
		// Flush as data arrives: dev servers stream (SSE, HMR, chunked SSR).
		FlushInterval: -1,
		Rewrite: func(proxied *httputil.ProxyRequest) {
			// "localhost", not an address: a dev server may listen on 127.0.0.1 or only on ::1.
			proxied.SetURL(&url.URL{Scheme: "http", Host: "localhost:" + strconv.Itoa(route.Port)})
			proxied.SetXForwarded()
			proxied.Out.Host = proxied.In.Host

			if route.HostHeader != "" {
				proxied.Out.Host = route.HostHeader
			}
		},
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, err error) {
			writer.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(writer, "run-stack: %s's %s (localhost:%d) is not answering.\n\n%v\n\nIt may still be starting: run-stack logs %s\n",
				route.Stack, route.Name, route.Port, err, route.Stack)
		},
	}

	proxy.ServeHTTP(writer, request)
}

func (router *router) notFound(writer http.ResponseWriter, host string) {
	router.mu.RLock()
	known := make([]string, 0, len(router.routes))

	for served := range router.routes {
		known = append(known, config.Origin(served, router.httpsPort))
	}

	router.mu.RUnlock()
	sort.Strings(known)

	if len(known) == 0 {
		known = []string{"(nothing)"}
	}

	writer.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(writer, "run-stack: no running stack serves %s\n\nRunning:\n  %s\n\nDashboard: %s\n",
		host, strings.Join(known, "\n  "), config.Origin("stack."+config.DefaultTLD(), router.httpsPort))
}
