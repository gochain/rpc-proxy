package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type Prox struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func NewProxy(target string) *Prox {
	url, _ := url.Parse(target)

	return &Prox{target: url, proxy: httputil.NewSingleHostReverseProxy(url)}
}

func (p *Prox) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-rpc-proxy", "rpc-proxy")
	p.proxy.Transport = &myTransport{}

	p.proxy.ServeHTTP(w, r)

}

var port *string
var redirecturl *string
var allowedPathes *string
var requestsPerMinuteLimit *int
var globalMap = make(map[string]MonitoringPath)

func main() {
	const (
		defaultPort                   = "8545"
		defaultPortUsage              = "default server port, ':8545'"
		defaultTarget                 = "http://127.0.0.1:8040"
		defaultTargetUsage            = "redirect url, 'http://127.0.0.1:8040'"
		defaultAllowedPath            = "eth*,net_*"
		defaultAllowedPathUsage       = "list of allowed pathes(separated by commas) - 'eth*,net_*'"
		defaultRequestsPerMinute      = 1000
		defaultRequestsPerMinuteUsage = "limit for number of requests per minute from single IP"
	)

	// flags
	port = flag.String("port", defaultPort, defaultPortUsage)
	redirecturl = flag.String("url", defaultTarget, defaultTargetUsage)
	allowedPathes = flag.String("allow", defaultAllowedPath, defaultAllowedPathUsage)
	requestsPerMinuteLimit = flag.Int("rpm", defaultRequestsPerMinute, defaultRequestsPerMinuteUsage)

	flag.Parse()

	log.Println("server will run on :", *port)
	log.Println("redirecting to :", *redirecturl)
	log.Println("list of allowed pathes :", *allowedPathes)
	log.Println("requests from IP per minute limited to :", *requestsPerMinuteLimit)

	// filling matcher rules
	err := AddMatcherRules(strings.Split(*allowedPathes, ","))
	if err != nil {
		log.Println("Cannot parse list of allowed pathes", err)
	}
	// proxy
	proxy := NewProxy(*redirecturl)

	http.HandleFunc("/rpc-proxy-server-status", ServerStatus)

	// server redirection
	http.HandleFunc("/", proxy.handle)
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}

func ServerStatus(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(getStats()))
	return
}
