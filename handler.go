package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blendle/zapdriver"

	"go.uber.org/zap"

	"github.com/gochain-io/gochain/v3/goclient"
	"github.com/gochain-io/gochain/v3/rpc"
)

type myTransport struct {
	lgr             *zap.Logger
	blockRangeLimit uint64 // 0 means none

	matcher
	limiters

	stats   map[string]MonitoringPath
	statsMu sync.RWMutex

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
		r.Body.Close() //closing reader
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
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))
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

func jsonRPCResponse(httpCode int, v interface{}) (*http.Response, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize JSON: %v", err)
	}
	return &http.Response{
		Body:       ioutil.NopCloser(bytes.NewReader(body)),
		StatusCode: httpCode,
	}, nil
}

func (t *myTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	parsedRequests, err := parseRequests(request)
	if err != nil {
		t.lgr.Error("Failed to parse requests", zap.Error(err))
		return nil, err
	}
	id := rand.Int31()
	lgr := t.lgr.With(zap.Int32("id", id))
	if verboseLogging {
		lgr.Info("Request", zapdriver.HTTP(zapdriver.NewHTTP(request, nil)))
	}
	start := time.Now()

	var union *blockRange
	for _, parsedRequest := range parsedRequests {
		if allowed, added := t.AllowVisitor(parsedRequest); !allowed {
			if verboseLogging {
				lgr.Info("IP is rate-limited", zap.String("ip", parsedRequest.RemoteAddr))
			}
			resp, err := jsonRPCResponse(http.StatusTooManyRequests, jsonRPCLimit(parsedRequest.ID))
			if err != nil {
				lgr.Error("Failed to construct rate-limit response", zap.Error(err))
				return nil, err
			}
			return resp, nil
		} else if added {
			lgr.Info("Added new visitor", zap.String("ip", parsedRequest.RemoteAddr))
		}

		if !t.MatchAnyRule(parsedRequest) {
			lgr.Info("Method not allowed", zap.String("ip", parsedRequest.RemoteAddr))
			resp, err := jsonRPCResponse(http.StatusMethodNotAllowed, jsonRPCUnauthorized(parsedRequest.ID, parsedRequest.Path))
			if err != nil {
				lgr.Error("Failed to construct not-allowed response", zap.Error(err))
				return nil, err
			}
			return resp, nil
		}
		if t.blockRangeLimit > 0 && parsedRequest.Path == "eth_getLogs" {
			r, invalid, err := t.parseRange(request.Context(), parsedRequest)
			if err != nil {
				resp, err := jsonRPCResponse(http.StatusInternalServerError, jsonRPCError(parsedRequest.ID, jsonRPCInternal, err.Error()))
				if err != nil {
					lgr.Error("Failed to construct internal error response", zap.Error(err))
					return nil, err
				}
				return resp, nil
			} else if invalid != nil {
				resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCError(parsedRequest.ID, jsonRPCInvalidParams, invalid.Error()))
				if err != nil {
					lgr.Error("Failed to construct invalid params response", zap.Error(err))
					return nil, err
				}
				return resp, nil
			}
			if r != nil {
				if l := r.len(); l > t.blockRangeLimit {
					// This request exceeds the block range limit.
					resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCBlockRangeLimit(parsedRequest.ID, l, t.blockRangeLimit))
					if err != nil {
						lgr.Error("Failed to construct block range limit response", zap.Error(err))
						return nil, err
					}
					return resp, nil
				}
				if union == nil {
					union = r
				} else {
					union.extend(r)
					if l := union.len(); l > t.blockRangeLimit {
						// The union of the ranges so far exceeds the block range limit.
						resp, err := jsonRPCResponse(http.StatusBadRequest, jsonRPCBlockRangeLimit(parsedRequest.ID, l, t.blockRangeLimit))
						if err != nil {
							lgr.Error("Failed to construct block range limit response", zap.Error(err))
							return nil, err
						}
						return resp, nil
					}
				}
			}
		}
	}
	request.Host = request.RemoteAddr //workaround for CloudFlare
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		lgr.Error("RoundTrip error", zap.Error(err))
		returnErrorCode := http.StatusBadGateway
		if response != nil {
			returnErrorCode = response.StatusCode
		}
		return jsonRPCResponse(returnErrorCode, jsonRPCError(parsedRequests[0].ID, jsonRPCInternal, err.Error()))
	}

	elapsed := time.Since(start)
	if verboseLogging {
		lgr.Info("Request completed", zap.Duration("runtime", elapsed))
	}
	for _, parsedRequest := range parsedRequests {
		t.updateStats(parsedRequest, elapsed)
	}
	return response, nil
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
