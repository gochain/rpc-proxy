package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var visitors = make(map[string]*rate.Limiter)
var mtx sync.Mutex

func per(eventCount int, duration time.Duration) rate.Limit {
	return rate.Every(duration / time.Duration(eventCount))
}

func addVisitor(ip string) *rate.Limiter {
	limiter := rate.NewLimiter(per(*requestsPerMinuteLimit, time.Minute), 10)
	log.Println("Added new visitor:", ip, " limit ", fmt.Sprint(*requestsPerMinuteLimit))
	mtx.Lock()
	visitors[ip] = limiter
	mtx.Unlock()
	return limiter
}

func getVisitor(ip string) *rate.Limiter {
	mtx.Lock()
	limiter, exists := visitors[ip]
	mtx.Unlock()
	if !exists {
		return addVisitor(ip)
	}
	return limiter
}

func AllowLimit(r ModifiedRequest) bool {
	limiter := getVisitor(r.RemoteAddr)
	if limiter.Allow() == false {
		log.Println("Exceeds limiter's burst path: ", r.Path, " ip: ", r.RemoteAddr)
		return false
	}
	return true
}
