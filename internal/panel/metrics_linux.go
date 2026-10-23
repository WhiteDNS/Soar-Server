//go:build linux

package panel

import (
	"bufio"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
)

func (s *hostSampler) platform(v map[string]any) {
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		v["file_descriptors_used"] = len(entries)
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err == nil {
		v["file_descriptors_limit"] = limit.Cur
	}
	for name, path := range map[string]string{
		"conntrack_used":  "/proc/sys/net/netfilter/nf_conntrack_count",
		"conntrack_limit": "/proc/sys/net/netfilter/nf_conntrack_max",
	} {
		if raw, err := os.ReadFile(path); err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
				v[name] = n
			}
		}
	}
	if raw, e := os.ReadFile("/proc/stat"); e == nil {
		line := strings.SplitN(string(raw), "\n", 2)[0]
		fields := strings.Fields(line)
		var total, idle uint64
		for i, f := range fields[1:] {
			n, _ := strconv.ParseUint(f, 10, 64)
			if i < 8 {
				total += n
			}
			if i == 3 || i == 4 {
				idle += n
			}
		}
		if s.total > 0 && total > s.total {
			v["cpu_percent"] = 100 * (1 - float64(idle-s.idle)/float64(total-s.total))
		}
		s.total = total
		s.idle = idle
	}
	if f, e := os.Open("/proc/meminfo"); e == nil {
		defer f.Close()
		scan := bufio.NewScanner(f)
		values := map[string]uint64{}
		for scan.Scan() {
			fields := strings.Fields(scan.Text())
			if len(fields) > 1 {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				values[strings.TrimSuffix(fields[0], ":")] = n * 1024
			}
		}
		v["memory_total"] = values["MemTotal"]
		v["memory_used"] = values["MemTotal"] - values["MemAvailable"]
	}
	var stat unix.Statfs_t
	if unix.Statfs("/", &stat) == nil {
		v["disk_total"] = stat.Blocks * uint64(stat.Bsize)
		v["disk_used"] = (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	}
}
