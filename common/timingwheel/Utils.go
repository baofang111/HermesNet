package timingwheel

import (
	"container/list"
	"sync"
	"time"
	"unsafe"
)

type waitGroupWrapper struct {
	sync.WaitGroup
}

func truncate(tickMs, m int64) int64 {
	if m <= 0 {
		return tickMs
	}
	return tickMs - tickMs%m
}

// timeToMs returns an integer number, which represents t in milliseconds.
func timeToMs(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

// Timer represents a single event. When the Timer expires, the given
// task will be executed.
type Timer struct {
	expiration int64 // in milliseconds
	task       func()

	// The bucket that holds the list to which this timer's element belongs.
	//
	// NOTE: This field may be updated and read concurrently,
	// through Timer.Stop() and Bucket.Flush().
	b unsafe.Pointer // type: *bucket

	// The timer's element.
	element *list.Element
}
