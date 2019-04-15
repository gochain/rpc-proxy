package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"

	"github.com/go-chi/chi"
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
	responseUnauthorized, err := json.MarshalIndent(jsonRPCUnauthorized(id, "method_name"), "", "  ")
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

func (p *Server) Example(w http.ResponseWriter, r *http.Request) {
	method := chi.URLParam(r, "method")
	do := func(params ...interface{}) {
		data, err := p.example(method, params...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(data)
	}
	param := chi.URLParam(r, "param")
	switch method {
	case "clique_getSigners":
		do("latest")
	case "clique_getSnapshot":
		do("latest")
	case "clique_getVoters":
		do("latest")
	case "eth_blockNumber":
		do("latest")
	case "eth_chainId":
		do()
	case "eth_gasPrice":
		do()
	case "eth_genesisAlloc":
		do()
	case "eth_getBalance":
		addr := "0x2c9c3FF339e1a38340987cd7fc5982Be7E39936d"
		if param != "" {
			addr = param
		}
		do(addr)
	case "eth_getBlockByHash":
		hash := "0x2c9c3FF339e1a38340987cd7fc5982Be7E39936d"
		if param != "" {
			hash = param
		}
		do(hash, false)
	case "eth_getBlockByNumber":
		var num interface{} = "latest"
		if param != "" {
			i, ok := new(big.Int).SetString(param, 10)
			if ok {
				num = fmt.Sprintf("0x%X", i)
			} else {
				num = param
			}
		}
		do(num, false)
	case "eth_getTransactionCount":
		hash := "0x2c9c3FF339e1a38340987cd7fc5982Be7E39936d"
		if param != "" {
			hash = param
		}
		do(hash, "latest")
	case "eth_getTransactionByHash", "eth_getTransactionReceipt":
		hash := "0x2c9c3FF339e1a38340987cd7fc5982Be7E39936d"
		if param != "" {
			hash = param
		}
		do(hash)
	case "eth_totalSupply":
		do("latest")
	case "net_listening":
		do()
	case "net_version":
		do()
	case "rpc_modules":
		do()

	default:
		http.NotFound(w, r)
	}
}

func (p *Server) example(method string, params ...interface{}) ([]byte, error) {
	body, err := json.Marshal(struct {
		JSONRPC string        `json:"jsonrpc"`
		ID      string        `json:"id"`
		Method  string        `json:"method"`
		Params  []interface{} `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, p.target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	const contentType = "application/json"
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := exampleTmpl.Execute(&buf, &exampleData{Method: method, Request: indent(body), Response: indent(respBody)}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func indent(b []byte) string {
	var buf bytes.Buffer
	_ = json.Indent(&buf, b, "", "  ")
	return buf.String()
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
				width:max-content;
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

		<p>Only the following listed methods are allowed. Click for an example. If you attempt to call any other methods, you will receive a 401 response:</p>

		<pre class="json">{{.ResponseUnauthorized}}</pre>

		<ul>
			{{range .Methods}}<li><a href="x/{{.}}">{{.}}</a></li>{{end}}
		</ul>
	<body>
</html>
`))

type exampleData struct {
	Method, Request, Response string
}

var exampleTmpl = template.Must(template.New("").Parse(`<!DOCTYPE html>
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
				width:max-content;
			}
		</style>
		<link href="https://fonts.googleapis.com/css?family=Open+Sans:400,300,600,700&amp;subset=all" rel="stylesheet" type="text/css">
	</head>
	<body>
		<h1>GoChain RPC Proxy</h1>

		<p>This is an example call for <code>{{.Method}}</code>.</p>

		<h2>Request</h2>

		<pre class="json">{{.Request}}</pre>

		<h2>Response</h2>

		<pre class="json">{{.Response}}</pre>
	<body>
</html>
`))
