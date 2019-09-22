package main

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type limiters struct {
	noLimitIPs map[string]struct{} // concurrent read safe after init.
	visitors   map[string]*rate.Limiter
	sync.RWMutex
}

func (ls *limiters) tryAddVisitor(ip string) (*rate.Limiter, bool) {
	ls.Lock()
	defer ls.Unlock()
	limiter, exists := ls.visitors[ip]
	if exists {
		return limiter, false
	}
	limit := rate.Every(time.Minute / time.Duration(requestsPerMinuteLimit))
	limiter = rate.NewLimiter(limit, requestsPerMinuteLimit/10)
	ls.visitors[ip] = limiter
	return limiter, true
}

func (ls *limiters) getVisitor(ip string) (*rate.Limiter, bool) {
	ls.RLock()
	limiter, exists := ls.visitors[ip]
	ls.RUnlock()
	if !exists {
		return ls.tryAddVisitor(ip)
	}
	return limiter, false
}

func (ls *limiters) AllowVisitor(r ModifiedRequest) (allowed, added bool) {
	if _, ok := ls.noLimitIPs[r.RemoteAddr]; ok {
		return true, false
	}
	limiter, added := ls.getVisitor(r.RemoteAddr)
	return limiter.Allow(), added
}
