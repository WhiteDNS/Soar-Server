package panel

import (
	"io"
	"time"
)

// UDP packets must remain atomic; the TCP limiter's chunking would corrupt
// QUIC and truncate DNS responses. The same durable account budget is used.
func (c *meterConn) readDatagram(p []byte) (int, error) {
	info, err := c.manager.store.Get(c.rt.client.ID)
	if err != nil || info.Status(time.Now()) != "active" || c.rt.ctx.Err() != nil {
		return 0, ErrDenied
	}
	n, err := c.Conn.Read(p)
	if n == 0 {
		return n, err
	}
	if e := c.pace(n, info.RateBytes, true); e != nil {
		return 0, e
	}
	if c.rt.ctx.Err() != nil {
		return 0, ErrDenied
	}
	allowed, epoch, e := c.manager.store.reserveDatagramEpoch(info.ID, n, false, c.rt.client.UsageEpoch)
	if e != nil {
		return 0, e
	}
	if allowed != n {
		if e := c.manager.store.refund(info.ID, allowed, false, epoch); e != nil {
			return 0, e
		}
		return 0, ErrDenied
	}
	return n, err
}

func (c *meterConn) writeDatagram(p []byte) (int, error) {
	info, err := c.manager.store.Get(c.rt.client.ID)
	if err != nil || info.Status(time.Now()) != "active" || c.rt.ctx.Err() != nil {
		return 0, ErrDenied
	}
	if err = c.pace(len(p), info.RateBytes, false); err != nil {
		return 0, err
	}
	if c.rt.ctx.Err() != nil {
		return 0, ErrDenied
	}
	allowed, epoch, err := c.manager.store.reserveDatagramEpoch(info.ID, len(p), true, c.rt.client.UsageEpoch)
	if err != nil {
		return 0, err
	}
	if allowed != len(p) {
		if err := c.manager.store.refund(info.ID, allowed, true, epoch); err != nil {
			return 0, err
		}
		return 0, ErrDenied
	}
	n, err := c.Conn.Write(p)
	if e := c.manager.store.refund(info.ID, allowed-n, true, epoch); e != nil {
		c.Close()
		return n, e
	}
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
