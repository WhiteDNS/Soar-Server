package panel

import "sync/atomic"

type EngineDiagnostics struct {
	Starts        uint64 `json:"starts"`
	Running       int64  `json:"running"`
	StartErrors   uint64 `json:"start_errors"`
	QueueDrops    uint64 `json:"queue_drops"`
	ActiveStreams int64  `json:"active_streams"`
	OpenedStreams uint64 `json:"opened_streams"`
	ClosedStreams uint64 `json:"closed_streams"`
}

type engineCounters struct {
	starts, startErrors, queueDrops, opened, closed atomic.Uint64
	running, active                                 atomic.Int64
}

func (c *runtimeCounters) forAdditional(name string) *engineCounters {
	if name == "masterdns" {
		return &c.additional[1]
	}
	return &c.additional[0]
}
