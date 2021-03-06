/*
定时器
基于linux时间轮实现

//参考skynet：https://github.com/cloudwu/skynet/blob/master/skynet-src/skynet_timer.c
http://blog.csdn.net/yueguanghaidao/article/details/46290539
skynet 定时器源码接卸：http://www.tuicool.com/articles/qui2ia
*/

package timer

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

const (
	TIME_NEAR_SHIFT  = 8
	TIME_NEAR        = 1 << TIME_NEAR_SHIFT //最小的分盘 256个刻度
	TIME_LEVEL_SHIFT = 6
	TIME_LEVEL       = 1 << TIME_LEVEL_SHIFT //其他分盘，64个刻度
	TIME_NEAR_MASK   = TIME_NEAR - 1         //255
	TIME_LEVEL_MASK  = TIME_LEVEL - 1        //63
)

type Timer struct {
	near [TIME_NEAR]*list.List
	t    [4][TIME_LEVEL]*list.List
	sync.Mutex
	time uint32
	tick time.Duration //定时器的tick
	quit chan struct{} //关闭定时器的chan
}

type Node struct {
	expire uint32
	f      func()
}

func (n *Node) String() string {
	return fmt.Sprintf("Node:expire,%d", n.expire)
}

/////////////////////////////////////////////////////////
// 初始化定时器
func NewTimer(d time.Duration) *Timer {
	t := new(Timer)
	t.time = 0
	t.tick = d
	t.quit = make(chan struct{})

	var i, j int
	for i = 0; i < TIME_NEAR; i++ {
		t.near[i] = list.New()
	}

	for i = 0; i < 4; i++ {
		for j = 0; j < TIME_LEVEL; j++ {
			t.t[i][j] = list.New()
		}
	}

	return t
}

func (t *Timer) addNode(n *Node) {
	expire := n.expire
	current := t.time
	// expire | TIME_NEAR_MASK 是为了把时间差值范围固定到 0-255之间
	if (expire | TIME_NEAR_MASK) == (current | TIME_NEAR_MASK) {
		fmt.Println("near-------", n)
		t.near[expire&TIME_NEAR_MASK].PushBack(n)
	} else {
		var i uint32
		var mask uint32 = TIME_NEAR << TIME_LEVEL_SHIFT
		for i = 0; i < 3; i++ {
			if (expire | (mask - 1)) == (current | (mask - 1)) {
				break
			}
			mask <<= TIME_LEVEL_SHIFT
		}

		t.t[i][(expire>>(TIME_NEAR_SHIFT+i*TIME_LEVEL_SHIFT))&TIME_LEVEL_MASK].PushBack(n)
		fmt.Println("t-------", n, i, (expire>>(TIME_NEAR_SHIFT+i*TIME_LEVEL_SHIFT))&TIME_LEVEL_MASK)
	}

}

// 添加定时任务
func (t *Timer) AddTimer(d time.Duration, f func()) *Node {
	n := new(Node)
	n.f = f
	t.Lock()
	n.expire = uint32(d/t.tick) + t.time // 这里表示需要几个tick，把d换算成tick数量
	t.addNode(n)
	t.Unlock()
	return n
}

func (t *Timer) String() string {
	return fmt.Sprintf("Timer:time:%d, tick:%s", t.time, t.tick)
}

func dispatchList(front *list.Element) {
	for e := front; e != nil; e = e.Next() {
		node := e.Value.(*Node)
		go node.f()
	}
}

func (t *Timer) moveList(level, idx int) {
	vec := t.t[level][idx]
	front := vec.Front()
	vec.Init()
	for e := front; e != nil; e = e.Next() {
		node := e.Value.(*Node)
		t.addNode(node)
	}
}

func (t *Timer) shift() {
	t.Lock()
	var mask uint32 = TIME_NEAR //256
	t.time++
	ct := t.time
	if ct == 0 {
		t.moveList(3, 0)
	} else {
		time := ct >> TIME_NEAR_SHIFT // 这里除以256，例如 515/256=2(取整了)，表示有多少个最小轮
		var i int = 0
		for (ct & (mask - 1)) == 0 { // 这里是模256，等于0表示可以整除，例如 515%256=3
			idx := int(time & TIME_LEVEL_MASK) // 模64
			if idx != 0 {                      // 如果这里等于，表示time圈数能被64整除，也就是说，总流逝的tick数把第一级的槽数都填满了，需要再找上一级的
				t.moveList(i, idx)
				break
			}
			mask <<= TIME_LEVEL_SHIFT // mask再乘以64
			time >>= TIME_LEVEL_SHIFT // 圈数再除以2^6=64
			i++
		}
	}
	t.Unlock()
}

func (t *Timer) execute() {
	t.Lock()
	idx := t.time & TIME_NEAR_MASK
	vec := t.near[idx]
	if vec.Len() > 0 {
		front := vec.Front()
		vec.Init()
		t.Unlock()
		// dispatch_list don't need lock
		dispatchList(front)
		return
	}

	t.Unlock()
}

func (t *Timer) update() {
	// try to dispatch timeout 0 (rare condition)
	t.execute()

	// shift time first, and then dispatch timer message
	t.shift()

	t.execute()

}

func (t *Timer) Start() {
	tick := time.NewTicker(t.tick)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			t.update()
		case <-t.quit:
			return
		}
	}
}

func (t *Timer) Stop() {
	close(t.quit)
}
