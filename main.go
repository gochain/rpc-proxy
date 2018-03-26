package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi"
	"github.com/go-chi/cors"
)

type Prox struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
	myTransport
}

func NewProxy(target string, m matcher) *Prox {
	url, _ := url.Parse(target)

	p := &Prox{target: url, proxy: httputil.NewSingleHostReverseProxy(url)}
	p.stats = make(map[string]MonitoringPath)
	p.matcher = m
	p.proxy.Transport = &p.myTransport
	return p
}

func (p *Prox) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-rpc-proxy", "rpc-proxy")
	p.proxy.ServeHTTP(w, r)
}

func (p *Prox) ServerStatus(w http.ResponseWriter, r *http.Request) {
	stats, err := p.getStats()
	if err != nil {
		http.Error(w, "failed to get stats", http.StatusInternalServerError)
		log.Println("Failed to get server stats:", err)
	} else {
		w.Write(stats)
	}
}

var port *string
var redirecturl *string
var allowedPathes *string
var requestsPerMinuteLimit *int

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
	rules, err := newMatcher(strings.Split(*allowedPathes, ","))
	if err != nil {
		log.Println("Cannot parse list of allowed paths", err)
	}
	// proxy
	proxy := NewProxy(*redirecturl, rules)

	r := chi.NewRouter()
	cors := cors.New(cors.Options{
		// AllowedOrigins: []string{"https://foo.com"}, // Use this to allow specific origin hosts
		AllowedOrigins: []string{"*"},
		// AllowOriginFunc:  func(r *http.Request, origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	})
	r.Use(cors.Handler)

	r.Get("/rpc-proxy-server-status", proxy.ServerStatus)
	r.HandleFunc("/", proxy.handle)
	log.Fatal(http.ListenAndServe(":"+*port, r))
}
