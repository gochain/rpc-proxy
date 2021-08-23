package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	toml "github.com/pelletier/go-toml"
	"github.com/rs/cors"
	"github.com/treeder/gcputils"
	"github.com/treeder/gotils/v2"
	"github.com/urfave/cli/v2"
)

var requestsPerMinuteLimit int

type ConfigData struct {
	Port            string   `toml:",omitempty"`
	URL             string   `toml:",omitempty"`
	WSURL           string   `toml:",omitempty"`
	Allow           []string `toml:",omitempty"`
	RPM             int      `toml:",omitempty"`
	NoLimit         []string `toml:",omitempty"`
	BlockRangeLimit uint64   `toml:",omitempty"`
}

func main() {
	ctx := context.Background()

	gotils.SetLoggable(gcputils.NewLogger())

	var configPath string
	var port string
	var redirecturl string
	var redirectWSUrl string
	var allowedPaths string
	var noLimitIPs string
	var blockRangeLimit uint64

	app := cli.NewApp()
	app.Name = "rpc-proxy"
	app.Usage = "A proxy for web3 JSONRPC"
	app.Version = Version

	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "config, c",
			Usage:       "path to toml config file",
			Destination: &configPath,
		},
		&cli.StringFlag{
			Name:        "port, p",
			Value:       "8545",
			Usage:       "port to serve",
			Destination: &port,
		},
		&cli.StringFlag{
			Name:        "url, u",
			Value:       "http://127.0.0.1:8040",
			Usage:       "redirect url",
			Destination: &redirecturl,
		},
		&cli.StringFlag{
			Name:        "wsurl, w",
			Value:       "ws://127.0.0.1:8041",
			Usage:       "redirect websocket url",
			Destination: &redirectWSUrl,
		},
		&cli.StringFlag{
			Name:        "allow, a",
			Usage:       "comma separated list of allowed paths",
			Destination: &allowedPaths,
		},
		&cli.IntFlag{
			Name:        "rpm",
			Value:       1000,
			Usage:       "limit for number of requests per minute from single IP",
			Destination: &requestsPerMinuteLimit,
		},
		&cli.StringFlag{
			Name:        "nolimit, n",
			Usage:       "list of ips allowed unlimited requests(separated by commas)",
			Destination: &noLimitIPs,
		},
		&cli.Uint64Flag{
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
		if redirectWSUrl != "" {
			if cfg.WSURL != "" {
				return errors.New("ws url set in two places")
			}
			cfg.WSURL = redirectWSUrl
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

		return cfg.run(ctx)
	}

	if err := app.Run(os.Args); err != nil {
		gotils.L(ctx).Error().Printf("Fatal error: %v", err)
		return
	}
	gotils.L(ctx).Info().Print("Shutting down")
}

func (cfg *ConfigData) run(ctx context.Context) error {
	sort.Strings(cfg.Allow)
	sort.Strings(cfg.NoLimit)

	gotils.L(ctx).Info().Println("Server starting, port:", cfg.Port, "redirectURL:", cfg.URL, "redirectWSURL:", cfg.WSURL,
		"rpmLimit:", cfg.RPM, "exempt:", cfg.NoLimit, "allowed:", cfg.Allow)

	// Create proxy server.
	server, err := cfg.NewServer()
	if err != nil {
		return fmt.Errorf("failed to start server: %s", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
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
	r.Get("/x/{method}", server.Example)
	r.Get("/x/{method}/{arg}", server.Example)
	r.Get("/x/{method}/{arg}/{arg2}", server.Example)
	r.Get("/x/{method}/{arg}/{arg2}/{arg3}", server.Example)
	r.Head("/x/net_version", func(w http.ResponseWriter, r *http.Request) {
		_, err := server.example("net_version")
		if err != nil {
			gotils.L(ctx).Error().Printf("Failed to ping RPC: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	r.HandleFunc("/*", server.RPCProxy)
	r.HandleFunc("/ws", server.WSProxy)
	return http.ListenAndServe(":"+cfg.Port, r)
}
