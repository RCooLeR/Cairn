package bus

import (
	"context"
	"time"
)

func CoalesceLatest(ctx context.Context, in <-chan Event, window time.Duration) <-chan Event {
	out := make(chan Event, 1)

	go func() {
		defer close(out)

		var (
			latest Event
			have   bool
			timer  *time.Timer
			timerC <-chan time.Time
		)

		stopTimer := func() {
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer = nil
				timerC = nil
			}
		}
		defer stopTimer()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-in:
				if !ok {
					if have {
						sendOrDone(ctx, out, latest)
					}
					return
				}
				latest = event
				have = true
				if timer == nil {
					timer = time.NewTimer(window)
					timerC = timer.C
				}
			case <-timerC:
				if have {
					if !sendOrDone(ctx, out, latest) {
						return
					}
					have = false
				}
				stopTimer()
			}
		}
	}()

	return out
}

func Batch(ctx context.Context, in <-chan Event, window time.Duration, maxN int) <-chan []Event {
	if maxN < 1 {
		maxN = 1
	}

	out := make(chan []Event, 1)

	go func() {
		defer close(out)

		batch := make([]Event, 0, maxN)
		var (
			timer  *time.Timer
			timerC <-chan time.Time
		)

		stopTimer := func() {
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer = nil
				timerC = nil
			}
		}
		defer stopTimer()

		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			next := append([]Event(nil), batch...)
			batch = batch[:0]
			stopTimer()
			return sendBatchOrDone(ctx, out, next)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-in:
				if !ok {
					flush()
					return
				}
				batch = append(batch, event)
				if timer == nil {
					timer = time.NewTimer(window)
					timerC = timer.C
				}
				if len(batch) >= maxN && !flush() {
					return
				}
			case <-timerC:
				if !flush() {
					return
				}
			}
		}
	}()

	return out
}

func sendOrDone(ctx context.Context, out chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func sendBatchOrDone(ctx context.Context, out chan<- []Event, batch []Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- batch:
		return true
	}
}
