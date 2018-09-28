package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"

	"golang.org/x/time/rate"
)

type Server struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
	myTransport
	homepage []byte
}

func NewServer(target string, allowedPaths []string, noLimitIPs []string) (*Server, error) {
	url, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	s := &Server{target: url, proxy: httputil.NewSingleHostReverseProxy(url)}
	s.stats = make(map[string]MonitoringPath)
	s.matcher, err = newMatcher(allowedPaths)
	if err != nil {
		return nil, err
	}
	s.visitors = make(map[string]*rate.Limiter)
	s.noLimitIPs = make(map[string]struct{})
	for _, ip := range noLimitIPs {
		s.noLimitIPs[ip] = struct{}{}
	}
	s.proxy.Transport = &s.myTransport

	// Generate static home page.
	id := json.RawMessage([]byte(`"ID"`))
	responseRateLimit, err := json.MarshalIndent(jsonRPCLimit(id), "", "  ")
	if err != nil {
		return nil, err
	}
	responseUnauthorized, err := json.MarshalIndent(jsonRPCUnauthorized(id, "<method_name>"), "", "  ")
	if err != nil {
		return nil, err
	}

	data := &homePageData{
		Limit:                requestsPerMinuteLimit,
		Methods:              allowedPaths,
		ResponseRateLimit:    string(responseRateLimit),
		ResponseUnauthorized: string(responseUnauthorized),
	}
	sort.Strings(data.Methods)

	var buf bytes.Buffer
	if err := homePageTmpl.Execute(&buf, &data); err != nil {
		return nil, err
	}
	s.homepage = buf.Bytes()

	return s, nil
}

func (p *Server) HomePage(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(w, bytes.NewReader(p.homepage)); err != nil {
		log.Printf("Failed to return homepage: %s", err)
	}
}

func (p *Server) RPCProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-rpc-proxy", "rpc-proxy")
	p.proxy.ServeHTTP(w, r)
}

func (p *Server) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := p.getStats()
	if err != nil {
		http.Error(w, "failed to get stats", http.StatusInternalServerError)
		log.Println("Failed to get server stats:", err)
	} else {
		w.Write(stats)
	}
}

type homePageData struct {
	Limit                int
	Methods              []string
	ResponseRateLimit    string
	ResponseUnauthorized string
}

var homePageTmpl = template.Must(template.New("").Parse(`<!DOCTYPE html>
<html lang="en">
	<head>
		<title>GoChain RPC Proxy</title>
		<style>
			body {
				font-family: 'Lato', sans-serif;
			}
			.json {
				padding: 1rem;
				padding-left: 2rem;
				background-color:#eee;
			}
		</style>
		<link href="https://fonts.googleapis.com/css?family=Open+Sans:400,300,600,700&amp;subset=all" rel="stylesheet" type="text/css">
	</head>
	<body>
		<h1>GoChain RPC Proxy</h1>

		<p>This is an RPC endpoint for <a href="https://gochain.io" rel="nofollow">GoChain</a>. It provides access to a limited subset of services. Rate limits apply.</p>

		<h2>Rate Limit</h2>

		<p>The rate limit is <code>{{.Limit}}</code> requests per minute. If you exceed this limit, you will receive a 429 response:</p>

		<pre class="json">{{.ResponseRateLimit}}</pre>

		<h2>Allowed Methods</h2>

		<p>Only the following listed methods are allowed. If you attempt to call any other methods, you will receive a 401 response:</p>

		<pre class="json">{{.ResponseUnauthorized}}</pre>

		<ul>
			{{range .Methods}}<li>{{.}}</li>{{end}}
		</ul>
	<body>
</html>
`))
