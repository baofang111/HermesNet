package timingwheel

import (
	"sync/atomic"
	"time"
	"unsafe"
)

/*
时间轮 实体类

	step1：时间被划分成 wheelSize 个槽，每个槽的间隔时间是 tick 毫秒。
	step2: 当时间轮启动时，随着时间的推移，时间轮的指针逐渐移动，每当指针移动到一个新的槽，就会触发该槽中的所有任务。
	step3: 如果某个任务的时间超出了当前时间轮的范围，则将该任务放到 overflowWheel 中管理，这是一种层次化的时间管理方式。
*/
type TimingWheel struct {

	// 表示时间轮中每一个槽的时间跨度 （ms s 等），每个 tick 代表时间轮中的最小时间单位，当有定时任务达到 该 tick 时，任务将会被触发
	// 时间轮类似于一个时钟，每次时间轮"走一格"，都会消耗 tick 毫秒。
	tick int64

	// 时间轮的大小 即时间轮包含的槽数，wheelSize 决定了每次轮回的最大时间跨度
	// 比如一个时钟有 60 格，意味着整个时钟的轮回是 60 个槽，这里相当于时间轮的大小。
	wheelSize int64

	// 每一圈时间轮的总时间跨度  = tick * wheelSize
	// 这个字段表示时间轮每次从开始到结束一圈所消耗的时间总量。
	interval int64

	// 当前时间轮的时间，单位 ms
	// 表示时间轮当前的“指针”指向哪个时间点。当有定时任务时，时间轮从当前时间出发，依次检查各个槽位是否有到期的任务。
	currentTime int64

	// 存储时间任务的 槽，每一个 bucket 存放多个时间任务，bucket 的数量 = wheelSize 的大小
	// 这些槽本质上是一个数组，每个槽可以存放多个到达同一个时间点的任务。当时间轮"走到"某个槽时，所有任务被处理。
	buckets []*Bucket

	// 用来管理各个槽的执行顺序，DelayQueue 是一个优先级的队列，存储了所有需要执行的延时任务
	// 当时间轮到达某个时间点时，任务会被放入 DelayQueue 中，按照到期时间的顺序进行处理。
	queue *DelayQueue

	// 指向更高级别的时间轮，当有任务超出当前时间轮的时候，会被放入到更高级别的时间轮当中
	// 任务的时间大于当前时间轮的范围时，任务会被添加到更大的时间轮中。这个字段使用 unsafe.Pointer 是为了在多线程环境中能高效地并发访问。
	overflowWheel unsafe.Pointer

	// 用于控制时间轮停止，通过向 exitC 发送信号，就可以优雅的关闭时间轮
	// 这是一个信号通道，用于时间轮算法的退出机制，确保时间轮中的任务能够安全停止。
	exitC chan struct{}

	// 协程同步控制，确保当时间轮停止的时候，宿友与时间轮相关的任何和协程都已经执行完毕
	// 这是 Go 的 sync.WaitGroup 的包装，用来等待所有协程任务完成后再退出。
	waitGroup waitGroupWrapper
}

/*
* 创建 时间轮
 */
func initTimeWheel(tickMs int64, wheelSize int64, startMs int64, queue *DelayQueue) *TimingWheel {
	buckets := make([]*Bucket, wheelSize)

	for i := range buckets {
		buckets[i] = NewBucket()
	}

	return &TimingWheel{
		tick:        tickMs,
		wheelSize:   wheelSize,
		interval:    tickMs * wheelSize,
		currentTime: truncate(startMs, tickMs),
		buckets:     buckets,
		queue:       queue,
		exitC:       make(chan struct{}),
	}
}

func newTimeWheel(tick time.Duration, wheelSize int64) *TimingWheel {
	tickMs := int64(tick / time.Millisecond)
	if tickMs <= 0 {
		panic("tickMs must be greater than 0")
	}

	startMs := timeToMs(time.Now().UTC())

	return initTimeWheel(tickMs, wheelSize, startMs, NewDelayqueue(int(wheelSize)))
}

func (tw *TimingWheel) add(t *Timer) bool {
	curentTime := atomic.LoadInt64(&tw.currentTime)

	// 检查定时器是否过期
	if t.expiration < curentTime+tw.tick {
		// 已经过期了
		return false
	} else if t.expiration < curentTime+tw.interval {
		// 将定时器放入到当前桶中
		virtualID := t.expiration / tw.tick
		bucket := tw.buckets[virtualID%tw.wheelSize]
		bucket.Add(t)

		if bucket.SetExpiration(virtualID * tw.tick) {
			tw.queue.Offer(bucket, bucket.Expiration())
		}

		return true
	} else {
		// 超过了 第一个时间轮，就重新生成一个，第二层的时间轮
		overflowWheel := atomic.LoadPointer(&tw.overflowWheel)
		// 如果为 空，就创建一个
		if overflowWheel == nil {
			atomic.CompareAndSwapPointer(
				&tw.overflowWheel,
				nil,
				unsafe.Pointer(initTimeWheel(
					tw.tick,
					tw.wheelSize,
					curentTime,
					tw.queue,
				)),
			)
			overflowWheel = atomic.LoadPointer(&tw.overflowWheel)
		}
		return (*TimingWheel)(overflowWheel).add(t)
	}
}

func (tw *TimingWheel) addOrRun(t *Timer) {
	if !tw.add(t) {
		go t.task()
	}
}

/*
前进时钟
*/
func (tw *TimingWheel) advanceClock(expiration int64) {
	currentTime := atomic.LoadInt64(&tw.currentTime)
	if expiration >= currentTime+tw.tick {
		currentTime = truncate(expiration, tw.tick)
		atomic.StoreInt64(&tw.currentTime, currentTime)

		overflowWheel := atomic.LoadPointer(&tw.overflowWheel)
		if overflowWheel == nil {
			(*TimingWheel)(overflowWheel).advanceClock(expiration)
		}
	}
}
