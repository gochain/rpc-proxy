package main

import (
	"encoding/json"
	"log"
	"time"
)

type MonitoringPath struct {
	Path          string
	Count         float64
	TotalDuration float64
	AverageTime   float64
}

func updateStats(parsedRequest ModifiedRequest, elapsed time.Duration) {
	key := parsedRequest.RemoteAddr + "-" + parsedRequest.Path
	if val, ok := globalMap[key]; ok {
		val.Count = val.Count + 1
		val.TotalDuration += elapsed.Seconds()
		val.AverageTime = val.TotalDuration / val.Count
		globalMap[key] = val
	} else {
		var m MonitoringPath
		m.Path = parsedRequest.Path
		m.Count = 1
		m.TotalDuration = elapsed.Seconds()
		m.AverageTime = m.TotalDuration / m.Count
		globalMap[key] = m
	}
}

func getStats() string {
	b, err := json.MarshalIndent(globalMap, "", "  ")
	if err != nil {
		log.Println("error:", err)
	}
	return string(b)
}
