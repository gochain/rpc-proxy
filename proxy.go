package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/gochain/gochain/v3/common"
	"github.com/treeder/gotils/v2"
	"golang.org/x/time/rate"
)

type Server struct {
	target  *url.URL
	proxy   *httputil.ReverseProxy
	wsProxy *WebsocketProxy
	myTransport
	homepage []byte
}

func (cfg *ConfigData) NewServer() (*Server, error) {
	url, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, err
	}
	wsurl, err := url.Parse(cfg.WSURL)
	if err != nil {
		return nil, err
	}
	s := &Server{target: url, proxy: httputil.NewSingleHostReverseProxy(url), wsProxy: NewProxy(wsurl)}
	s.myTransport.blockRangeLimit = cfg.BlockRangeLimit
	s.myTransport.url = cfg.URL
	s.matcher, err = newMatcher(cfg.Allow)
	if err != nil {
		return nil, err
	}
	s.visitors = make(map[string]*rate.Limiter)
	s.noLimitIPs = make(map[string]struct{})
	for _, ip := range cfg.NoLimit {
		s.noLimitIPs[ip] = struct{}{}
	}
	s.proxy.Transport = &s.myTransport
	s.wsProxy.Transport = &s.myTransport

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
		Methods:              cfg.Allow,
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
	ctx := r.Context()
	if _, err := io.Copy(w, bytes.NewReader(p.homepage)); err != nil {
		gotils.L(ctx).Error().Printf("Failed to serve homepage: %v", err)
		return
	}
}

func (p *Server) RPCProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-rpc-proxy", "rpc-proxy")
	p.proxy.ServeHTTP(w, r)
}

func (p *Server) WSProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-rpc-proxy", "rpc-proxy")
	p.wsProxy.ServeHTTP(w, r)
}

func (p *Server) Example(w http.ResponseWriter, r *http.Request) {
	method := chi.URLParam(r, "method")
	args := []string{
		chi.URLParam(r, "arg"),
		chi.URLParam(r, "arg2"),
		chi.URLParam(r, "arg3"),
	}
	do := func(params ...func(string) (interface{}, error)) {
		var fmtd []interface{}
		for i, p := range params {
			if i > len(args) {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if p == nil {
				fmtd = append(fmtd, args[i])
				continue
			}
			arg, err := p(args[i])
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fmtd = append(fmtd, arg)
		}
		data, err := p.example(method, fmtd...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(data)
	}
	switch method {
	case "clique_getSigners":
		do(hexNumOrLatest)
	case "clique_getSignersAtHash":
		do(hexHash)
	case "clique_getSnapshot":
		do(hexNumOrLatest)
	case "clique_getSnapshotAtHash":
		do(hexHash)
	case "clique_getVoters":
		do(hexNumOrLatest)
	case "clique_getVotersAtHash":
		do(hexHash)
	case "eth_blockNumber":
		do(hexNumOrLatest)
	case "eth_chainId":
		do()
	case "eth_gasPrice":
		do()
	case "eth_genesisAlloc":
		do()
	case "eth_getBalance":
		do(hexAddr, hexNumOrLatest)
	case "eth_getBlockByHash":
		do(hexHash, boolOrFalse)
	case "eth_getBlockByNumber":
		do(hexNumOrLatest, boolOrFalse)
	case "eth_getBlockTransactionCountByHash":
		do(hexHash)
	case "eth_getBlockTransactionCountByNumber":
		do(hexNumOrLatest)
	case "eth_getCode":
		do(hexAddr, hexNumOrLatest)
	case "eth_getFilterChanges":
		do(nil)
	case "eth_getLogs":
		do(func(arg string) (interface{}, error) {
			if hasHexPrefix(arg) {
				arg = arg[2:]
			}
			if !isHex(arg) {
				return nil, fmt.Errorf("non-hex argument: %s", arg)
			}
			return map[string]interface{}{"blockhash": "0x" + arg}, nil
		})
	case "eth_getStorageAt":
		do(hexAddr, hexNumOrZero, hexNumOrLatest)
	case "eth_getTransactionByBlockHashAndIndex":
		do(nil, hexNumOrZero)
	case "eth_getTransactionByBlockNumberAndIndex":
		do(hexNumOrLatest, hexNumOrZero)
	case "eth_getTransactionCount":
		do(hexAddr, hexNumOrLatest)
	case "eth_getTransactionByHash", "eth_getTransactionReceipt":
		do(hexHash)
	case "eth_totalSupply":
		do(hexNumOrLatest)
	case "net_listening":
		do()
	case "net_version":
		do()
	case "rpc_modules":
		do()
	case "web3_clientVersion":
		do()

	default:
		http.NotFound(w, r)
	}
}

func hexAddr(arg string) (interface{}, error) {
	if !common.IsHexAddress(arg) {
		return nil, fmt.Errorf("not a hex address: %s", arg)
	}
	return arg, nil
}

func isHexHash(s string) bool {
	if hasHexPrefix(s) {
		s = s[2:]
	}
	return len(s) == 2*common.HashLength && isHex(s)
}

func hexHash(arg string) (interface{}, error) {
	if !isHexHash(arg) {
		return nil, fmt.Errorf("not a hex hash: %s", arg)
	}
	return arg, nil
}

func boolOrFalse(arg string) (interface{}, error) {
	if arg == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(arg)
	if err != nil {
		return nil, fmt.Errorf("not a bool: %v", err)
	}
	return v, nil
}

func hexNumOrLatest(arg string) (interface{}, error) {
	return hexNumOr(arg, "latest", "pending", "earliest")
}

func hexNumOrZero(arg string) (interface{}, error) {
	return hexNumOr(arg, "0x0")
}

// hexNumOr reforms decimal integers as '0x' prefixed hex and returns
// or for empty, otherwise an error is returned.
func hexNumOr(arg string, or string, allow ...string) (interface{}, error) {
	if arg == "" {
		return or, nil
	}
	for _, a := range allow {
		if arg == a {
			return arg, nil
		}
	}
	i, ok := new(big.Int).SetString(arg, 0)
	if !ok {
		return nil, fmt.Errorf("not an integer: %s", arg)
	}
	return fmt.Sprintf("0x%X", i), nil
}

// hasHexPrefix validates str begins with '0x' or '0X'.
func hasHexPrefix(str string) bool {
	return len(str) >= 2 && str[0] == '0' && (str[1] == 'x' || str[1] == 'X')
}

// isHexCharacter returns bool of c being a valid hexadecimal.
func isHexCharacter(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// isHex validates whether each byte is valid hexadecimal string.
func isHex(str string) bool {
	if len(str)%2 != 0 {
		return false
	}
	for _, c := range []byte(str) {
		if !isHexCharacter(c) {
			return false
		}
	}
	return true
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
	var formattedResp string
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		formattedResp = fmt.Sprintf("Code: %d (%s)\nBody: %s\n", resp.StatusCode, resp.Status, string(respBody))
	} else {
		formattedResp = indent(respBody)
	}

	var buf bytes.Buffer
	if err := exampleTmpl.Execute(&buf, &exampleData{Method: method, Request: indent(body), Response: formattedResp}); err != nil {
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
