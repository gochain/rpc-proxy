package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type myTransport struct {
	matcher
	stats   map[string]MonitoringPath
	statsMu sync.RWMutex
}

type ModifiedRequest struct {
	Path       string
	RemoteAddr string
}

//RPC request
type rpcRequest struct {
	Method string
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

func parseRequests(r *http.Request) []ModifiedRequest {
	var res []ModifiedRequest
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if r.Body != nil {
		bodyBytes, err := ioutil.ReadAll(r.Body)
		r.Body.Close() //closing reader
		if err == nil {
			if isBatch(bodyBytes) {
				var arr []rpcRequest
				err = json.Unmarshal(bodyBytes, &arr)
				if err == nil {
					for _, t := range arr {
						res = append(res, ModifiedRequest{
							Path:       t.Method,
							RemoteAddr: ip,
						})
					}
				} else {
					log.Println("cannot parse JSON single request", "err", err.Error(), r)
				}
			} else {
				var t rpcRequest
				err = json.Unmarshal(bodyBytes, &t)
				if err == nil {
					res = append(res, ModifiedRequest{
						Path:       t.Method,
						RemoteAddr: ip,
					})
				} else {
					log.Println("cannot parse JSON batch request", "err", err.Error(), r)
				}
			}
		} else {
			log.Println("cannot read body", "err", err.Error(), r)
		}
		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
	}
	if len(res) == 0 {
		res = append(res, ModifiedRequest{
			Path:       r.URL.Path,
			RemoteAddr: ip,
		})
	}

	return res
}

func (t *myTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var response *http.Response
	var err error
	start := time.Now()
	parsedRequests := parseRequests(request)

	for _, parsedRequest := range parsedRequests {
		if !AllowLimit(parsedRequest) {
			log.Println("User hit the limit:", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)
			return &http.Response{
				Body:       ioutil.NopCloser(bytes.NewBufferString("You hit the request limit")),
				StatusCode: http.StatusTooManyRequests,
			}, nil
		}

		if !t.MatchAnyRule(parsedRequest) {
			log.Println("Not allowed:", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)
			return &http.Response{
				Body:       ioutil.NopCloser(bytes.NewBufferString("You are not authorized to make this request")),
				StatusCode: http.StatusUnauthorized,
			}, nil
		}

		response, err = http.DefaultTransport.RoundTrip(request)
		if err != nil {
			print("\n\ncame in error resp here", err)
			return &http.Response{
				Body:       ioutil.NopCloser(bytes.NewBufferString("Internal error")),
				StatusCode: http.StatusInternalServerError,
			}, err
		}
	}
	elapsed := time.Since(start)

	for _, parsedRequest := range parsedRequests {
		t.updateStats(parsedRequest, elapsed)
		log.Println("Response Time:", elapsed.Seconds(), " path: ", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)
	}
	return response, err
}
