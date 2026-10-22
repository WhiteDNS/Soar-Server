package panel

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

const usageBatchSize = 128
const usageBatchDelay = 250 * time.Microsecond

var errNoUsageChanges = errors.New("no usage changes")

type usageRequest struct {
	id                   string
	n                    int
	up, datagram, refund bool
	epoch                uint64
	done                 chan usageResult
}
type usageResult struct {
	allowed int
	epoch   uint64
	err     error
}
type usageBatcher struct {
	db                          *bolt.DB
	mu                          sync.RWMutex
	closed                      bool
	jobs                        chan usageRequest
	done                        chan struct{}
	requests, commits, failures atomic.Uint64
}

func newUsageBatcher(db *bolt.DB) *usageBatcher {
	b := &usageBatcher{db: db, jobs: make(chan usageRequest, 1024), done: make(chan struct{})}
	go b.run()
	return b
}

// Every caller waits for the shared transaction to commit before forwarding
// bytes. Group commit amortizes disk synchronization without an unbilled window.
func (b *usageBatcher) submit(r usageRequest) usageResult {
	r.done = make(chan usageResult, 1)
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return usageResult{err: bolt.ErrDatabaseNotOpen}
	}
	b.jobs <- r
	b.mu.RUnlock()
	return <-r.done
}

func (b *usageBatcher) close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.jobs)
	}
	b.mu.Unlock()
	<-b.done
}

func (b *usageBatcher) run() {
	defer close(b.done)
	for first := range b.jobs {
		batch := []usageRequest{first}
		timer := time.NewTimer(usageBatchDelay)
	collect:
		for len(batch) < usageBatchSize {
			select {
			case r, ok := <-b.jobs:
				if !ok {
					break collect
				}
				batch = append(batch, r)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		b.commit(batch)
	}
}

func (b *usageBatcher) commit(batch []usageRequest) {
	results := make([]usageResult, len(batch))
	b.requests.Add(uint64(len(batch)))
	err := b.db.Update(func(tx *bolt.Tx) error {
		// This cache lasts only one write transaction. Metadata edits and usage
		// resets therefore always serialize against the latest committed policy.
		accounts := make(map[string]Client)
		dirty := make(map[string]bool)
		for i, r := range batch {
			c, ok := accounts[r.id]
			if !ok {
				var err error
				c, err = getClient(tx, r.id)
				if err != nil {
					results[i].err = err
					continue
				}
				accounts[r.id] = c
			}
			if r.refund {
				if c.UsageEpoch != r.epoch {
					continue
				}
				if r.up {
					c.UploadBytes = max(0, c.UploadBytes-int64(r.n))
				} else {
					c.DownloadBytes = max(0, c.DownloadBytes-int64(r.n))
				}
			} else {
				if c.Status(time.Now()) != "active" || r.epoch != 0 && c.UsageEpoch != r.epoch {
					results[i].err = ErrDenied
					continue
				}
				n := min(r.n, 32*1024)
				if r.datagram {
					n = r.n
				}
				if c.QuotaBytes > 0 {
					remaining := c.QuotaBytes - c.Used()
					if r.datagram && int64(n) > remaining {
						results[i].err = ErrDenied
						continue
					}
					n = min(n, int(remaining))
				}
				if n <= 0 {
					results[i].err = ErrDenied
					continue
				}
				if c.Used() > 1<<60 {
					results[i].err = errors.New("usage counter capacity reached")
					continue
				}
				if r.up {
					c.UploadBytes += int64(n)
				} else {
					c.DownloadBytes += int64(n)
				}
				c.LastSeen = time.Now().UnixMilli()
				results[i].allowed, results[i].epoch = n, c.UsageEpoch
			}
			accounts[r.id], dirty[r.id] = c, true
		}
		for id := range dirty {
			if err := putJSON(tx.Bucket(clients), []byte(id), accounts[id]); err != nil {
				return err
			}
		}
		if len(dirty) == 0 {
			return errNoUsageChanges
		}
		return nil
	})
	if errors.Is(err, errNoUsageChanges) {
		err = nil
	} else if err != nil {
		b.failures.Add(1)
	} else {
		b.commits.Add(1)
	}
	for i, r := range batch {
		if err != nil {
			results[i] = usageResult{err: err}
		}
		r.done <- results[i]
	}
}

func (s *Store) AccountingStats() map[string]uint64 {
	return map[string]uint64{"requests": s.usage.requests.Load(), "commits": s.usage.commits.Load(), "failed_commits": s.usage.failures.Load(), "queued": uint64(len(s.usage.jobs))}
}
