package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blendle/zapdriver"
	"github.com/go-chi/chi/middleware"
	"github.com/gochain-io/gochain/v3/goclient"
	"github.com/gochain-io/gochain/v3/rpc"
	"go.uber.org/zap"
)

type myTransport struct {
	lgr             *zap.Logger
	blockRangeLimit uint64 // 0 means none

	matcher
	limiters

	latestBlock
}

type ModifiedRequest struct {
	Path       string
	RemoteAddr string // Original IP, not CloudFlare or load balancer.
	ID         json.RawMessage
	Params     []json.RawMessage
}

func isBatch(msg []byte) bool {
	for _, c := range msg {
		if c == 0x20 || c == 0x09 || c == 0x0a || c == 0x0d {
			continue
		}
		return c == '['
	}
	return false
}

// getIP returns the original IP address from the request, checking special headers before falling back to RemoteAddr.
func getIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// Trim off any others: A.B.C.D[,X.X.X.X,Y.Y.Y.Y,]
		return strings.SplitN(ip, ",", 1)[0]
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

func parseRequests(r *http.Request) ([]ModifiedRequest, error) {
	var res []ModifiedRequest
	ip := getIP(r)
	if r.Body != nil {
		body, err := ioutil.ReadAll(r.Body)
		r.Body.Close()
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body)) // must be done, even when err
		if err != nil {
			return nil, fmt.Errorf("failed to read body: %v", err)
		}
		type rpcRequest struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if isBatch(body) {
			var arr []rpcRequest
			err = json.Unmarshal(body, &arr)
			if err != nil {
				return nil, fmt.Errorf("failed to parse JSON batch request: %v", err)
			}
			for _, t := range arr {
				res = append(res, ModifiedRequest{
					ID:         t.ID,
					Path:       t.Method,
					RemoteAddr: ip,
					Params:     t.Params,
				})
			}
		} else {
			var t rpcRequest
			err = json.Unmarshal(body, &t)
			if err != nil {
				return nil, fmt.Errorf("failed to parse JSON request: %v", err)
			}
			res = append(res, ModifiedRequest{
				ID:         t.ID,
				Path:       t.Method,
				RemoteAddr: ip,
				Params:     t.Params,
			})
		}
	}
	if len(res) == 0 {
		res = append(res, ModifiedRequest{
			Path:       r.URL.Path,
			RemoteAddr: ip,
		})
	}
	return res, nil
}

const (
	jsonRPCTimeout       = -32000
	jsonRPCUnavailable   = -32601
	jsonRPCInvalidParams = -32602
	jsonRPCInternal      = -32603
)

func jsonRPCError(id json.RawMessage, jsonCode int, msg string) interface{} {
	type errResponse struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	resp := errResponse{
		Version: "2.0",
		ID:      id,
	}
	resp.Error.Code = jsonCode
	resp.Error.Message = msg
	return resp
}

func jsonRPCUnauthorized(id json.RawMessage, method string) interface{} {
	return jsonRPCError(id, jsonRPCUnavailable, "You are not authorized to make this request: "+method)
}

func jsonRPCLimit(id json.RawMessage) interface{} {
	return jsonRPCError(id, jsonRPCTimeout, "You hit the request limit")
}

func jsonRPCBlockRangeLimit(id json.RawMessage, blocks, limit uint64) interface{} {
	return jsonRPCError(id, jsonRPCInvalidParams, fmt.Sprintf("Requested range of blocks (%d) is larger than limit (%d).", blocks, limit))
}

// jsonRPCResponse returns a JSON response containing v, or a plaintext generic
// response for this httpCode and an error when JSON marshalling fails.
func jsonRPCResponse(httpCode int, v interface{}) (*http.Response, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return &http.Response{
			Body:       ioutil.NopCloser(strings.NewReader(http.StatusText(httpCode))),
			StatusCode: httpCode,
		}, fmt.Errorf("failed to serialize JSON: %v", err)
	}
	return &http.Response{
		Body:       ioutil.NopCloser(bytes.NewReader(body)),
		StatusCode: httpCode,
	}, nil
}

func (t *myTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	lgr := t.lgr
	if reqID := middleware.GetReqID(req.Context()); reqID != "" {
		lgr = lgr.With(zap.String("requestID", reqID))
	}

	parsedRequests, err := parseRequests(req)
	if err != nil {
		lgr.Error("Failed to parse requests", zap.Error(err))
		resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCError(json.RawMessage("1"), jsonRPCInvalidParams, err.Error()))
		if err != nil {
			lgr.Error("Failed to construct invalid params response", zap.Error(err))
		}
		return resp, nil
	}
	methods := make([]string, len(parsedRequests))
	for i := range parsedRequests {
		methods[i] = parsedRequests[i].Path
	}
	lgr = lgr.With(zap.Strings("methods", methods))
	if blockResponse := t.block(req.Context(), lgr, parsedRequests); blockResponse != nil {
		return blockResponse, nil
	}
	lgr.Info("Forwarding request")
	req.Host = req.RemoteAddr //workaround for CloudFlare
	return http.DefaultTransport.RoundTrip(req)
}

// block returns a response only if the request should be blocked, otherwise it returns nil if allowed.
func (t *myTransport) block(ctx context.Context, lgr *zap.Logger, parsedRequests []ModifiedRequest) *http.Response {
	var union *blockRange
	for _, parsedRequest := range parsedRequests {
		lgr := lgr.With(zap.String("ip", parsedRequest.RemoteAddr))
		if allowed, added := t.AllowVisitor(parsedRequest); !allowed {
			lgr.Info("Request blocked: Rate limited")
			resp, err := jsonRPCResponse(http.StatusTooManyRequests, jsonRPCLimit(parsedRequest.ID))
			if err != nil {
				lgr.Error("Failed to construct rate-limit response", zap.Error(err))
			}
			return resp
		} else if added {
			lgr.Info("Added new visitor", zap.String("ip", parsedRequest.RemoteAddr))
		}

		if !t.MatchAnyRule(parsedRequest.Path) {
			lgr.Info("Request blocked: Method not allowed")
			resp, err := jsonRPCResponse(http.StatusMethodNotAllowed, jsonRPCUnauthorized(parsedRequest.ID, parsedRequest.Path))
			if err != nil {
				lgr.Error("Failed to construct not-allowed response", zap.Error(err))
			}
			return resp
		}
		if t.blockRangeLimit > 0 && parsedRequest.Path == "eth_getLogs" {
			r, invalid, err := t.parseRange(ctx, parsedRequest)
			if err != nil {
				resp, err := jsonRPCResponse(http.StatusInternalServerError, jsonRPCError(parsedRequest.ID, jsonRPCInternal, err.Error()))
				if err != nil {
					lgr.Error("Failed to construct internal error response", zap.Error(err))
				}
				return resp
			} else if invalid != nil {
				lgr.Info("Request blocked: Invalid params", zap.Error(invalid))
				resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCError(parsedRequest.ID, jsonRPCInvalidParams, invalid.Error()))
				if err != nil {
					lgr.Error("Failed to construct invalid params response", zap.Error(err))
				}
				return resp
			}
			if r != nil {
				if l := r.len(); l > t.blockRangeLimit {
					lgr.Info("Request blocked: Exceeds block range limit", zap.Uint64("range", l), zap.Uint64("limit", t.blockRangeLimit))
					resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCBlockRangeLimit(parsedRequest.ID, l, t.blockRangeLimit))
					if err != nil {
						lgr.Error("Failed to construct block range limit response", zap.Error(err))
					}
					return resp
				}
				if union == nil {
					union = r
				} else {
					union.extend(r)
					if l := union.len(); l > t.blockRangeLimit {
						lgr.Info("Request blocked: Exceeds block range limit", zap.Uint64("range", l), zap.Uint64("limit", t.blockRangeLimit))
						resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCBlockRangeLimit(parsedRequest.ID, l, t.blockRangeLimit))
						if err != nil {
							lgr.Error("Failed to construct block range limit response", zap.Error(err))
						}
						return resp
					}
				}
			}
		}
	}
	return nil
}

type blockRange struct{ start, end uint64 }

func (b blockRange) len() uint64 {
	return b.end - b.start + 1
}

func (b *blockRange) extend(b2 *blockRange) {
	if b2.start < b.start {
		b.start = b2.start
	}
	if b2.end > b.end {
		b.end = b2.end
	}
}

// parseRange returns a block range if one exists, or an error if the request is invalid.
func (t *myTransport) parseRange(ctx context.Context, request ModifiedRequest) (r *blockRange, invalid, internal error) {
	if len(request.Params) == 0 {
		return nil, nil, nil
	}
	type filterQuery struct {
		BlockHash *string          `json:"blockHash"`
		FromBlock *rpc.BlockNumber `json:"fromBlock"`
		ToBlock   *rpc.BlockNumber `json:"toBlock"`
	}
	var fq filterQuery
	err := json.Unmarshal(request.Params[0], &fq)
	if err != nil {
		return nil, err, nil
	}
	if fq.BlockHash != nil {
		return nil, nil, nil
	}
	var start, end uint64
	if fq.FromBlock != nil {
		switch *fq.FromBlock {
		case rpc.LatestBlockNumber, rpc.PendingBlockNumber:
			l, err := t.latestBlock.get(ctx)
			if err != nil {
				return nil, nil, err
			}
			start = l
		default:
			start = uint64(*fq.FromBlock)
		}
	}
	if fq.ToBlock == nil {
		l, err := t.latestBlock.get(ctx)
		if err != nil {
			return nil, nil, err
		}
		end = l
	} else {
		switch *fq.ToBlock {
		case rpc.LatestBlockNumber, rpc.PendingBlockNumber:
			l, err := t.latestBlock.get(ctx)
			if err != nil {
				return nil, nil, err
			}
			end = l
		default:
			end = uint64(*fq.ToBlock)
		}
	}

	return &blockRange{start: start, end: end}, nil, nil
}

type latestBlock struct {
	url    string
	client *goclient.Client

	mu sync.RWMutex // Protects everything below.

	next chan struct{} // Set when an update is running, and closed when the next result is available.

	num uint64
	err error
	at  *time.Time // When num and err were set.
}

func (l *latestBlock) get(ctx context.Context) (uint64, error) {
	l.mu.RLock()
	next, num, err, at := l.next, l.num, l.err, l.at
	l.mu.RUnlock()
	if at != nil && time.Since(*at) < 5*time.Second {
		return num, err
	}
	if next == nil {
		// No update in progress, so try to trigger one.
		next, num, err = l.update()
	}
	if next != nil {
		// Wait on update to complete.
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-next:
		}
		l.mu.RLock()
		num = l.num
		err = l.err
		l.mu.RUnlock()
	}

	return num, err

}

// update updates (num, err, at). Only one instance may run at a time, and it
// spot is reserved by setting next, which is closed when the operation completes.
// Returns a chan to wait on if another instance is already running. Otherwise
// returns num and err if the operation is complete.
func (l *latestBlock) update() (chan struct{}, uint64, error) {
	l.mu.Lock()
	if next := l.next; next != nil {
		// Someone beat us to it, return their next chan.
		l.mu.Unlock()
		return next, 0, nil
	}
	next := make(chan struct{})
	l.next = next
	l.mu.Unlock()

	var latest uint64
	var err error
	if l.client == nil {
		l.client, err = goclient.Dial(l.url)
	}
	if err == nil {
		var lBig *big.Int
		lBig, err = l.client.LatestBlockNumber(context.Background())
		if err == nil {
			latest = lBig.Uint64()
		}
	}
	now := time.Now()

	l.mu.Lock()
	l.num = latest
	l.err = err
	l.at = &now
	l.next = nil
	l.mu.Unlock()

	close(next)

	return nil, latest, err
}

var _ middleware.LogFormatter = &zapLogFormatter{}

type zapLogFormatter struct {
	lgr *zap.Logger
}

func (z *zapLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	h := NewHTTP(r, nil)
	lgr := z.lgr
	if reqID := middleware.GetReqID(r.Context()); reqID != "" {
		lgr = lgr.With(zap.String("requestID", reqID))
	}
	lgr.Info("Request started", zapdriver.HTTP(h))
	return &zapLogEntry{lgr: lgr, http: h}
}

var _ middleware.LogEntry = &zapLogEntry{}

type zapLogEntry struct {
	lgr  *zap.Logger
	http *zapdriver.HTTPPayload
}

func (z *zapLogEntry) Write(status, bytes int, elapsed time.Duration) {
	z.http.Status = status
	z.http.ResponseSize = strconv.Itoa(bytes)
	z.http.Latency = fmt.Sprintf("%.9fs", elapsed.Seconds())
	z.lgr.Info("Request complete", zapdriver.HTTP(z.http))
}

func (z *zapLogEntry) Panic(v interface{}, stack []byte) {
	z.lgr = z.lgr.With(zap.String("stack", string(stack)), zap.String("panic", fmt.Sprintf("%+v", v)))
}

// NewHTTP returns a new HTTPPayload struct, based on the passed
// in http.Request and http.Response objects. They are not modified
// in any way, unlike the zapdriver version this is based on.
func NewHTTP(req *http.Request, res *http.Response) *zapdriver.HTTPPayload {
	var p zapdriver.HTTPPayload
	if req != nil {
		p = zapdriver.HTTPPayload{
			RequestMethod: req.Method,
			UserAgent:     req.UserAgent(),
			RemoteIP:      req.RemoteAddr,
			Referer:       req.Referer(),
			Protocol:      req.Proto,
			RequestSize:   strconv.FormatInt(req.ContentLength, 10),
		}
		if req.URL != nil {
			p.RequestURL = req.URL.String()
		}
	}

	if res != nil {
		p.ResponseSize = strconv.FormatInt(res.ContentLength, 10)
		p.Status = res.StatusCode
	}

	return &p
}
