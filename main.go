package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blendle/zapdriver"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	toml "github.com/pelletier/go-toml"
	"github.com/rs/cors"
	"github.com/urfave/cli"
	"go.uber.org/zap"
)

var requestsPerMinuteLimit int

type ConfigData struct {
	Port            string   `toml:",omitempty"`
	URL             string   `toml:",omitempty"`
	Allow           []string `toml:",omitempty"`
	RPM             int      `toml:",omitempty"`
	NoLimit         []string `toml:",omitempty"`
	BlockRangeLimit uint64   `toml:",omitempty"`
}

func main() {
	start := time.Now()
	lgr, err := zapdriver.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer lgr.Sync()

	var configPath string
	var port string
	var redirecturl string
	var allowedPaths string
	var noLimitIPs string
	var blockRangeLimit uint64

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
		cli.Uint64Flag{
			Name:        "blocklimit, b",
			Usage:       "block range query limit",
			Destination: &blockRangeLimit,
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
		if blockRangeLimit > 0 {
			if cfg.BlockRangeLimit > 0 {
				return errors.New("block range limit set in two places")
			}
			cfg.BlockRangeLimit = blockRangeLimit
		}

		return cfg.run(lgr)
	}

	if err := app.Run(os.Args); err != nil {
		lgr.Fatal("Fatal error", zap.Error(err), zap.Duration("runtime", time.Since(start)))
	}
	lgr.Info("Shutting down", zap.Duration("runtime", time.Since(start)))
}

func (cfg *ConfigData) run(lgr *zap.Logger) error {
	sort.Strings(cfg.Allow)
	sort.Strings(cfg.NoLimit)

	lgr.Info("Server starting", zap.String("port", cfg.Port), zap.String("redirectURL", cfg.URL),
		zap.Int("rpmLimit", cfg.RPM), zap.Strings("exempt", cfg.NoLimit), zap.Strings("allowed", cfg.Allow))

	// Create proxy server.
	server, err := cfg.NewServer(lgr)
	if err != nil {
		return fmt.Errorf("failed to start server: %s", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RequestLogger(&zapLogFormatter{lgr: lgr}))
	r.Use(middleware.Recoverer)
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
	r.Get("/x/{method}/{arg}", server.Example)
	r.Get("/x/{method}/{arg}/{arg2}", server.Example)
	r.Get("/x/{method}/{arg}/{arg2}/{arg3}", server.Example)
	r.Head("/x/net_version", func(w http.ResponseWriter, r *http.Request) {
		_, err := server.example("net_version")
		if err != nil {
			lgr.Error("Failed to ping RPC", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	r.HandleFunc("/*", server.RPCProxy)
	return http.ListenAndServe(":"+cfg.Port, r)
}
