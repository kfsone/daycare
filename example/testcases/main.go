package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/kfsone/daycare"
)

type result struct {
	received int
	ok       bool
	value    interface{}
}

var wg sync.WaitGroup

func testOp(result *result, op func() (interface{}, bool, error)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		value, ok, err := op()
		if err != nil {
			panic(err)
		}
		result.ok = ok
		result.value = value
		result.received++
	}()
}

func testQuery(dc *daycare.Registry, result *result, key string) {
	testOp(result, func() (interface{}, bool, error) {
		value, exists, err := dc.Lookup(key)
		return value, exists, err
	})
}

func testRegister(dc *daycare.Registry, result *result, key string, value interface{}) {
	testOp(result, func() (interface{}, bool, error) {
		return dc.Register(key, value)
	})
}

func main() {
	dc := daycare.NewRegistry()
	dc.Start()

	var result1 result
	testQuery(dc, &result1, "monkey")
	time.Sleep(20 * time.Millisecond)
	if result1.received > 0 {
		panic("early receipt")
	}

	var result2 result
	testQuery(dc, &result2, "sheep")
	time.Sleep(20 * time.Millisecond)
	if result2.received > 0 {
		panic("early receipt #2")
	}

	// register none of the above
	var result3 result
	var valueFor3 = new(int)
	testRegister(dc, &result3, "biscuit", valueFor3)
	time.Sleep(20 * time.Millisecond)
	if result3.received != 1 {
		panic("failed registration")
	}
	if !result3.ok {
		panic("wrong ok")
	}
	if result3.value != valueFor3 {
		panic("wrong value received")
	}

	var result4 result
	var valueFor4 = new(string)
	testRegister(dc, &result4, "biscuit", valueFor4)
	time.Sleep(20 * time.Millisecond)
	if result4.received != 1 {
		panic("failed reregister")
	}
	if result4.value == valueFor4 {
		panic("reregistration went through")
	}
	if result4.value != valueFor3 {
		panic("wrong value in reregistration" + fmt.Sprintf("%#+v", result4.value))
	}
	if result4.ok {
		panic("wrong ok")
	}

	// if we now look-up 'biscuit', we should find it immediately.
	var result5 result
	testQuery(dc, &result5, "biscuit")
	time.Sleep(20 * time.Millisecond)
	if result5.received != 1 {
		panic("failed lookup biscut")
	}
	if result5.value != valueFor3 {
		panic("wrong value returned")
	}

	if result1.received > 0 || result2.received > 0 {
		panic("early responses")
	}

	// Now if we register monkey it should cause query #1 to resolve
	var result6 result
	var valueFor6 = new(bool)
	testRegister(dc, &result6, "monkey", valueFor6)
	time.Sleep(50 * time.Millisecond)
	if result6.received != 1 {
		panic("failed register monkey")
	}
	if result1.received != 1 {
		panic("failed notify monkey")
	}
	if result1.value != valueFor6 {
		panic("wrong value for monkey")
	}
	if result2.received != 0 {
		panic("received sheep")
	}
	if !result1.ok {
		panic("wrong ok for monkey")
	}

	// If we stop the manager, sheep should be notified that it didn't resolve
	result2.value = valueFor3
	dc.Stop()
	time.Sleep(50 * time.Millisecond)
	if result2.received != 1 {
		panic("wrong received count")
	}
	if result2.value != nil {
		panic("got a value for sheep")
	}
	if result2.ok {
		panic("got 'ok' signal on sheep")
	}
}
