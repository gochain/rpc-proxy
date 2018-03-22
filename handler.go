package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"
)

type myTransport struct {
}

type ModifiedRequest struct {
	Path       string
	Method     string
	RemoteAddr string
}

//RPC request
type rpcRequest struct {
	Method string
}

func parseRequest(r *http.Request) ModifiedRequest {
	path := r.URL.Path
	if r.Body != nil {
		bodyBytes, err := ioutil.ReadAll(r.Body)
		r.Body.Close() //closing reader
		if err == nil {
			var t rpcRequest
			err = json.Unmarshal(bodyBytes, &t)
			if err == nil {
				path = t.Method
			} else {
				log.Println("cannot parse JSON", "err", err.Error(), r)
			}
		} else {
			log.Println("cannot read body", "err", err.Error(), r)
		}
		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return ModifiedRequest{
		Path:       path,
		Method:     r.Method,
		RemoteAddr: ip,
	}
}

func (t *myTransport) RoundTrip(request *http.Request) (*http.Response, error) {

	start := time.Now()
	parsedRequest := parseRequest(request)

	if !MatchAnyRule(parsedRequest) {
		log.Println("Not allowed:", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)
		return &http.Response{
			Body:       ioutil.NopCloser(bytes.NewBufferString("You are not authorized to make this request")),
			StatusCode: http.StatusUnauthorized,
		}, nil
	}

	if !AllowLimit(parsedRequest) {
		log.Println("User hit the limit:", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)
		return &http.Response{
			Body:       ioutil.NopCloser(bytes.NewBufferString("You hit the request limit")),
			StatusCode: http.StatusTooManyRequests,
		}, nil
	}

	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		print("\n\ncame in error resp here", err)
		return &http.Response{
			Body:       ioutil.NopCloser(bytes.NewBufferString("Internal error")),
			StatusCode: http.StatusInternalServerError,
		}, err
	}
	elapsed := time.Since(start)
	updateStats(parsedRequest, elapsed)
	log.Println("Response Time:", elapsed.Seconds(), " path: ", parsedRequest.Path, " from IP: ", parsedRequest.RemoteAddr)

	return response, err
}
