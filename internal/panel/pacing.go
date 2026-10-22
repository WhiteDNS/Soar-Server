package panel

import (
	"net"
	"os"
	"time"
)

type pacingDeadlineError struct{}

func (pacingDeadlineError) Error() string   { return "bandwidth wait exceeded operation deadline" }
func (pacingDeadlineError) Timeout() bool   { return true }
func (pacingDeadlineError) Temporary() bool { return true }
func (pacingDeadlineError) Unwrap() error   { return os.ErrDeadlineExceeded }

var errPacingDeadline error = pacingDeadlineError{}

// Only the head of the account queue spends bandwidth time. Cancelled queued
// operations unlink in constant time and leave no debt for later connections.
type paceTicket struct {
	prev, next *paceTicket
	ready      chan struct{}
	readyAt    time.Time
	queued     bool
}

func (r *accountRuntime) queuePace() *paceTicket {
	t := &paceTicket{ready: make(chan struct{}), queued: true}
	r.rateMu.Lock()
	t.prev = r.rateTail
	if r.rateTail != nil {
		r.rateTail.next = t
	} else {
		r.rateHead = t
	}
	r.rateTail = t
	if r.rateHead == t {
		t.readyAt = time.Now()
		close(t.ready)
	}
	r.rateMu.Unlock()
	return t
}
func (r *accountRuntime) releasePace(t *paceTicket) {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if !t.queued {
		return
	}
	wasHead := r.rateHead == t
	if t.prev != nil {
		t.prev.next = t.next
	} else {
		r.rateHead = t.next
	}
	if t.next != nil {
		t.next.prev = t.prev
	} else {
		r.rateTail = t.prev
	}
	t.queued = false
	if wasHead && r.rateHead != nil {
		r.rateHead.readyAt = time.Now()
		close(r.rateHead.ready)
	}
}

func (c *meterConn) initIOStateLocked() {
	if c.done == nil {
		c.done = make(chan struct{})
	}
	if c.deadlineChanged == nil {
		c.deadlineChanged = make(chan struct{})
	}
}
func (c *meterConn) ioState(read bool) (time.Time, <-chan struct{}, <-chan struct{}, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.initIOStateLocked()
	deadline := c.writeDeadline
	if read {
		deadline = c.readDeadline
	}
	var err error
	if c.rt.ctx.Err() != nil {
		err = ErrDenied
	} else if c.closed {
		err = net.ErrClosed
	} else if !deadline.IsZero() && !time.Now().Before(deadline) {
		err = errPacingDeadline
	}
	return deadline, c.deadlineChanged, c.done, err
}
func (c *meterConn) setIODeadline(read, write bool, deadline time.Time) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.initIOStateLocked()
	if read {
		c.readDeadline = deadline
	}
	if write {
		c.writeDeadline = deadline
	}
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
}
func (c *meterConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetDeadline(t); err != nil {
		return err
	}
	c.setIODeadline(true, true, t)
	return nil
}
func (c *meterConn) SetReadDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return err
	}
	c.setIODeadline(true, false, t)
	return nil
}
func (c *meterConn) SetWriteDeadline(t time.Time) error {
	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return err
	}
	c.setIODeadline(false, true, t)
	return nil
}

func (c *meterConn) pace(n int, rate int64, read bool) error {
	if _, _, _, err := c.ioState(read); err != nil {
		return err
	}
	if rate <= 0 || n == 0 {
		return nil
	}
	ticket := c.rt.queuePace()
	defer c.rt.releasePace(ticket)
	ready := ticket.ready
	var finish time.Time
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		deadline, changed, done, err := c.ioState(read)
		if err != nil {
			return err
		}
		wakeAt := finish
		if !deadline.IsZero() && (wakeAt.IsZero() || deadline.Before(wakeAt)) {
			wakeAt = deadline
		}
		var timerC <-chan time.Time
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if !wakeAt.IsZero() {
			timer.Reset(max(time.Until(wakeAt), 0))
			timerC = timer.C
		}
		select {
		case <-c.rt.ctx.Done():
			return ErrDenied
		case <-done:
			return net.ErrClosed
		case <-changed:
		case <-ready:
			ready = nil
			finish = ticket.readyAt.Add(time.Duration(float64(n) / float64(rate) * float64(time.Second)))
		case <-timerC:
			if _, _, _, err := c.ioState(read); err != nil {
				return err
			}
			if !finish.IsZero() && !time.Now().Before(finish) {
				return nil
			}
		}
	}
}
