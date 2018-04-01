package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi"
	"github.com/go-chi/cors"
	"github.com/urfave/cli"
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

var requestsPerMinuteLimit int

func main() {

	var port string
	var redirecturl string
	var allowedPathes string

	app := cli.NewApp()

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "port, p",
			Value:       "8545",
			Usage:       "default server port, ':8545'",
			Destination: &port,
		},
		cli.StringFlag{
			Name:        "url, u",
			Value:       "http://127.0.0.1:8040",
			Usage:       "redirect url, default is http://127.0.0.1:8040",
			Destination: &redirecturl,
		},
		cli.StringFlag{
			Name:        "allow, a",
			Value:       "eth*,net_*",
			Usage:       "list of allowed pathes(separated by commas) - default is 'eth*,net_*'",
			Destination: &allowedPathes,
		},
		cli.IntFlag{
			Name:        "rpm",
			Value:       1000,
			Usage:       "limit for number of requests per minute from single IP(default it 1000)",
			Destination: &requestsPerMinuteLimit,
		},
	}

	app.Action = func(c *cli.Context) error {
		log.Println("server will run on :", port)
		log.Println("redirecting to :", redirecturl)
		log.Println("list of allowed pathes :", allowedPathes)
		log.Println("requests from IP per minute limited to :", requestsPerMinuteLimit)

		// filling matcher rules
		rules, err := newMatcher(strings.Split(allowedPathes, ","))
		if err != nil {
			log.Println("Cannot parse list of allowed paths", err)
		}
		// proxy
		proxy := NewProxy(redirecturl, rules)

		r := chi.NewRouter()
		cors := cors.New(cors.Options{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
			AllowCredentials: true,
			MaxAge:           300, // Maximum value not ignored by any of major browsers
		})
		r.Use(cors.Handler)

		r.Get("/rpc-proxy-server-status", proxy.ServerStatus)
		r.HandleFunc("/", proxy.handle)
		log.Fatal(http.ListenAndServe(":"+port, r))
		return nil
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
