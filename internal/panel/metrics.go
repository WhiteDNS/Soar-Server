package panel

import (
	"runtime"
	"sync"
)

type hostSampler struct {
	mu          sync.Mutex
	total, idle uint64
}

func (s *hostSampler) sample() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	v := map[string]any{"platform": runtime.GOOS, "cpu_percent": nil, "memory_total": nil, "memory_used": nil, "disk_total": nil, "disk_used": nil, "process_memory": m.Alloc, "goroutines": runtime.NumGoroutine()}
	s.platform(v)
	return v
}
