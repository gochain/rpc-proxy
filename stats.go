package main

import (
	"encoding/json"
	"time"
)

type MonitoringPath struct {
	Path          string
	Count         float64
	TotalDuration float64
	AverageTime   float64
}

func (t *myTransport) updateStats(parsedRequest ModifiedRequest, elapsed time.Duration) {
	key := parsedRequest.RemoteAddr + "-" + parsedRequest.Path
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	if val, ok := t.stats[key]; ok {
		val.Count = val.Count + 1
		val.TotalDuration += elapsed.Seconds()
		val.AverageTime = val.TotalDuration / val.Count
		t.stats[key] = val
	} else {
		var m MonitoringPath
		m.Path = parsedRequest.Path
		m.Count = 1
		m.TotalDuration = elapsed.Seconds()
		m.AverageTime = m.TotalDuration / m.Count
		t.stats[key] = m
	}
}

func (t *myTransport) getStats() ([]byte, error) {
	t.statsMu.RLock()
	defer t.statsMu.RUnlock()
	b, err := json.MarshalIndent(t.stats, "", "  ")
	if err != nil {
		return nil, err
	}
	return b, nil
}
