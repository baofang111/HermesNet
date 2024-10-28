package timingwheel

import (
	"container/list"
	"sync"
	"sync/atomic"
	"unsafe"
)

/*
时间轮的 bucket
*/
type Bucket struct {
	expiration int64

	mu     sync.Mutex
	timers *list.List
}

func NewBucket() *Bucket {
	return &Bucket{
		timers:     list.New(),
		expiration: -1,
	}
}

func (b *Bucket) Add(t *Timer) {
	b.mu.Lock()

	e := b.timers.PushBack(t)
	t.setBucket(b)
	t.element = e

	b.mu.Unlock()
}

func (t *Timer) setBucket(b *Bucket) {
	atomic.StorePointer(&t.b, unsafe.Pointer(b))
}

func (b *Bucket) SetExpiration(expiration int64) bool {
	return atomic.SwapInt64(&b.expiration, expiration) != expiration
}

func (b *Bucket) Expiration() int64 {
	return atomic.LoadInt64(&b.expiration)
}
