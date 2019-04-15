package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi"
	"github.com/pelletier/go-toml"
	"github.com/rs/cors"
	"github.com/urfave/cli"
)

var requestsPerMinuteLimit int
var verboseLogging bool

type ConfigData struct {
	Port    string   `toml:",omitempty"`
	URL     string   `toml:",omitempty"`
	Allow   []string `toml:",omitempty"`
	RPM     int      `toml:",omitempty"`
	NoLimit []string `toml:",omitempty"`
}

func main() {

	var configPath string
	var port string
	var redirecturl string
	var allowedPaths string
	var noLimitIPs string

	app := cli.NewApp()

	app.Name = "rpc-proxy"
	app.Usage = "A proxy for web3 JSONRPC"
	app.Version = Version

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "config, c",
			Usage:       "path to toml config file",
			Destination: &configPath,
		},
		cli.StringFlag{
			Name:        "port, p",
			Value:       "8545",
			Usage:       "port to serve",
			Destination: &port,
		},
		cli.StringFlag{
			Name:        "url, u",
			Value:       "http://127.0.0.1:8040",
			Usage:       "redirect url",
			Destination: &redirecturl,
		},
		cli.StringFlag{
			Name:        "allow, a",
			Usage:       "comma separated list of allowed paths",
			Destination: &allowedPaths,
		},
		cli.IntFlag{
			Name:        "rpm",
			Value:       1000,
			Usage:       "limit for number of requests per minute from single IP",
			Destination: &requestsPerMinuteLimit,
		},
		cli.StringFlag{
			Name:        "nolimit, n",
			Usage:       "list of ips allowed unlimited requests(separated by commas)",
			Destination: &noLimitIPs,
		},
		cli.BoolFlag{
			Name:        "verbose",
			Usage:       "verbose logging enabled",
			Destination: &verboseLogging,
		},
	}

	app.Action = func(c *cli.Context) error {
		var cfg ConfigData
		if configPath != "" {
			t, err := toml.LoadFile(configPath)
			if err != nil {
				return err
			}
			if err := t.Unmarshal(&cfg); err != nil {
				return err
			}
		}

		if port != "" {
			if cfg.Port != "" {
				return errors.New("port set in two places")
			}
			cfg.Port = port
		}
		if redirecturl != "" {
			if cfg.URL != "" {
				return errors.New("url set in two places")
			}
			cfg.URL = redirecturl
		}
		if requestsPerMinuteLimit != 0 {
			if cfg.RPM != 0 {
				return errors.New("rpm set in two places")
			}
			cfg.RPM = requestsPerMinuteLimit
		}
		if allowedPaths != "" {
			if len(cfg.Allow) > 0 {
				return errors.New("allow set in two places")
			}
			cfg.Allow = strings.Split(allowedPaths, ",")
		}
		if noLimitIPs != "" {
			if len(cfg.NoLimit) > 0 {
				return errors.New("nolimit set in two places")
			}
			cfg.NoLimit = strings.Split(noLimitIPs, ",")
		}

		sort.Strings(cfg.Allow)
		sort.Strings(cfg.NoLimit)

		log.Println("Server will run on port:", cfg.Port)
		log.Println("Redirecting to url:", cfg.URL)
		log.Println("Requests-per-minute for each IP limited to:", cfg.RPM)
		log.Println("List of IPs exempt from the limit:", cfg.NoLimit)
		log.Println("List of allowed paths:", cfg.Allow)

		// Create proxy server.
		server, err := NewServer(redirecturl, cfg.Allow, cfg.NoLimit)
		if err != nil {
			return fmt.Errorf("failed to start server: %s", err)
		}

		r := chi.NewRouter()
		// Use default options
		r.Use(cors.New(cors.Options{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"HEAD", "GET", "POST", "PUT", "PATCH", "DELETE"},
			AllowedHeaders:   []string{"*"},
			AllowCredentials: false,
			MaxAge:           3600,
		}).Handler)

		r.Get("/", server.HomePage)
		r.Head("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.Get("/stats", server.Stats)
		r.Get("/x/{method}", server.Example)
		r.Get("/x/{method}/{param}", server.Example)
		r.Head("/x/net_version", func(w http.ResponseWriter, r *http.Request) {
			_, err := server.example("net_version")
			if err != nil {
				log.Printf("Failed to ping rpc: %v\n", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		r.HandleFunc("/*", server.RPCProxy)
		log.Fatal(http.ListenAndServe(":"+port, r))
		return nil
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
