package panel

import (
	"sort"
	"time"
)

type resolverHealth struct {
	rtt      time.Duration
	failures int
	retryAt  time.Time
}
type resolverCandidate struct {
	endpoint string
	probe    bool
}

func (r *accountRuntime) orderedResolvers(endpoints []string, now time.Time) []resolverCandidate {
	r.resolverMu.Lock()
	defer r.resolverMu.Unlock()
	if r.resolverHealth == nil {
		r.resolverHealth = make(map[string]*resolverHealth)
	}
	allowed := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		allowed[endpoint] = true
	}
	for endpoint := range r.resolverHealth {
		if !allowed[endpoint] {
			delete(r.resolverHealth, endpoint)
		}
	}
	result := make([]resolverCandidate, len(endpoints))
	for i, endpoint := range endpoints {
		result[i].endpoint = endpoint
	}
	rank := func(endpoint string) int {
		h := r.resolverHealth[endpoint]
		if h == nil {
			return 1
		}
		if h.failures > 0 {
			return 2
		}
		return 0
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := rank(result[i].endpoint), rank(result[j].endpoint)
		if a != b {
			return a < b
		}
		if a == 0 {
			return r.resolverHealth[result[i].endpoint].rtt < r.resolverHealth[result[j].endpoint].rtt
		}
		return false
	})
	// A failed endpoint cannot delay every new query. Occasionally reserve one
	// short recovery probe; parallel requests keep using the healthy endpoint.
	if len(result) > 1 && rank(result[0].endpoint) == 0 {
		for i := 1; i < len(result); i++ {
			h := r.resolverHealth[result[i].endpoint]
			if h != nil && h.failures > 0 && !now.Before(h.retryAt) {
				h.retryAt = now.Add(30 * time.Second)
				probe := result[i]
				probe.probe = true
				copy(result[1:i+1], result[0:i])
				result[0] = probe
				break
			}
		}
	}
	return result
}
func (r *accountRuntime) resolverResult(endpoint string, elapsed time.Duration, success bool) {
	r.resolverMu.Lock()
	defer r.resolverMu.Unlock()
	if r.resolverHealth == nil {
		r.resolverHealth = make(map[string]*resolverHealth)
	}
	h := r.resolverHealth[endpoint]
	if h == nil {
		if len(r.resolverHealth) >= 32 {
			return
		}
		h = &resolverHealth{}
		r.resolverHealth[endpoint] = h
	}
	if success {
		if h.rtt == 0 {
			h.rtt = elapsed
		} else {
			h.rtt = (3*h.rtt + elapsed) / 4
		}
		h.failures = 0
		h.retryAt = time.Time{}
	} else {
		h.failures = min(h.failures+1, 5)
		h.retryAt = time.Now().Add(time.Duration(1<<uint(h.failures-1)) * 15 * time.Second)
	}
}
