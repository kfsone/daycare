package daycare

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const TestWaitTick = 100 * time.Microsecond
const TestShortWait = 100 * time.Millisecond
const TestLongWait = 250 * time.Millisecond

// Test the blockedness of a waitgroup within a given time window.
func testWaitGroup(t *testing.T, wg *sync.WaitGroup, getsDone bool, wait time.Duration) {
	t.Helper()
	done := false
	go func() {
		wg.Wait()
		done = true
	}()
	if getsDone {
		assert.Eventually(t, func() bool { return done == true }, wait, TestWaitTick)
	} else {
		assert.Never(t, func() bool { return done == true }, wait, TestWaitTick)
	}
}

// Create an array of channels and increment a counter by the number of channels allocated.
func channelList(n int, testCh chan interface{}, counter *int) (list []chan<- interface{}) {
	list = make([]chan<- interface{}, n)
	for i := 0; i < len(list); i++ {
		list[i] = testCh
	}
	*counter += n
	return list
}

// Tests whether an operation completes/remains blocked over a given time window. Any triggers that
// are supplied will imply the expectation that the operation should remain blocked until at least
// all the triggers have been executed.
func testTimed(t *testing.T, succeeds bool, operation func(), triggers ...func()) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		operation()
	}()
	for i := 0; i < len(triggers); i++ {
		testWaitGroup(t, &wg, false, TestShortWait)
		triggers[i]()
	}
	testWaitGroup(t, &wg, succeeds, TestShortWait)
}

// Try and receive from a channel, returns true/false whether there was data waiting. Data is discarded.
func tryChannel(c <-chan interface{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	if assert.NotNil(t, reg) {
		assert.NotNil(t, reg.registrations)
		assert.NotNil(t, reg.lookups)
		if assert.NotNil(t, reg.pending) {
			assert.Len(t, reg.pending, 0)
		}
		if assert.NotNil(t, reg.registry) {
			assert.Len(t, reg.registry, 0)
		}
		assert.Equal(t, Stats{}, reg.Stats)
		assert.Nil(t, reg.onDefer)
		assert.Nil(t, reg.onResolve)
	}
	// Make sure we can read/write the channels.
	t.Run("registrations channel", func(t *testing.T) {
		testTimed(t, true,
			func() { <-reg.registrations },
			func() { reg.registrations <- nil })
		assert.Equal(t, Stats{}, reg.Stats)
	})
	t.Run("lookups channel", func(t *testing.T) {
		testTimed(t, true,
			func() { <-reg.lookups },
			func() { reg.lookups <- nil })
		assert.Equal(t, Stats{}, reg.Stats)
	})
}

func Test_valueToPending(t *testing.T) {
	t.Parallel()
	t.Run("noop", func(t *testing.T) {
		t.Parallel()
		// Sending to an empty list with no waiters should take no time.
		var wg sync.WaitGroup
		var pending = make([]chan<- interface{}, 0)
		valueToPending(&wg, pending, nil, nil)
		testWaitGroup(t, &wg, true, TestLongWait)
	})
	t.Run("dispatch", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		var channels = make([]chan interface{}, 3)
		var pending = make([]chan<- interface{}, 3)
		for i := 0; i < len(channels); i++ {
			channels[i] = make(chan interface{})
			pending[i] = channels[i]
		}
		// valueToPending should run in the background.
		var value = &struct{ i int }{i: 42}
		valueToPending(&wg, pending, value, nil)
		// until we receive the first entry, the 3rd should block
		t.Run("blocks", func(t *testing.T) {
			t.Run("order", func(t *testing.T) {
				//t.Parallel()
				assert.Never(t, func() bool { return tryChannel(channels[len(channels)-1]) }, TestLongWait, TestWaitTick)
			})
			t.Run("waitgroup", func(t *testing.T) {
				//t.Parallel()
				testWaitGroup(t, &wg, false, TestLongWait)
			})
		})
		t.Run("sends", func(t *testing.T) {
			t.Run("receivers", func(t *testing.T) {
				//t.Parallel()
				for i := 0; i < len(pending); i++ {
					i := i
					t.Run(fmt.Sprintf("rx#%d", i), func(t *testing.T) {
						assert.Eventually(t, func() bool { return tryChannel(channels[i]) }, TestLongWait, TestWaitTick)
					})
				}
			})
			t.Run("waitgroup", func(t *testing.T) {
				t.Parallel()
				assert.Eventually(t, func() bool { wg.Wait(); return true }, TestLongWait, TestWaitTick)
			})
		})
	})
	t.Run("handler", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		var channel = make(chan<- interface{}, 8)
		var count int
		var pending = []chan<- interface{}{channel, channel, channel, channel}

		valueToPending(&wg, pending, nil, func(i int) { count = i })

		testTimed(t, true, wg.Wait)
		assert.Equal(t, 4, count)
	})
}

func TestRegistry_closePending(t *testing.T) {
	t.Parallel()
	t.Run("empty list", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.closePending()
		testWaitGroup(t, &reg.waitgroup, true, TestShortWait)
		if assert.NotNil(t, reg.pending) {
			assert.Len(t, reg.pending, 0)
		}
		assert.Equal(t, Stats{}, reg.Stats)
	})

	t.Run("populated list", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		// create one channel and count the receives.
		const MaxChannels = 1024
		testCh := make(chan interface{}, MaxChannels)
		channelCount := 0

		// create a large mapping with multiple channels to receive to
		// 8 keys
		reg.pending["one"] = channelList(7, testCh, &channelCount)
		reg.pending["two"] = channelList(1, testCh, &channelCount)
		reg.pending["three"] = channelList(11, testCh, &channelCount)
		reg.pending["four"] = channelList(53, testCh, &channelCount)
		reg.pending["five"] = channelList(15, testCh, &channelCount)
		reg.pending["a"] = channelList(17, testCh, &channelCount)
		reg.pending["z"] = channelList(9, testCh, &channelCount)
		reg.pending["q"] = channelList(31, testCh, &channelCount)
		require.Len(t, reg.pending, 8)
		require.LessOrEqual(t, channelCount, MaxChannels)

		// close pending should queue up a bunch of work and then delete
		// all the keys leaving r.pending empty but populated, while
		// the signals all get sent. Meanwhile, the workers should be
		// blocked because we're not receiving yet and we didn't make
		// these non-blocking channels.
		testWaitGroup(t, &reg.waitgroup, true, TestShortWait)
		reg.closePending()
		if assert.NotNil(t, reg.pending) {
			if assert.Len(t, reg.pending, 0) {
				testWaitGroup(t, &reg.waitgroup, true, TestLongWait)
			}
		}

		// receive away, but they should all be 'unregisteredValue'.
		rxCount, rxDone := 0, false
		go func() {
			for v := range testCh {
				require.Equal(t, unregisteredValue, v)
				rxCount++
				assert.LessOrEqual(t, rxCount, channelCount)
			}
			rxDone = true
		}()
		assert.Eventually(t, func() bool { return rxCount == channelCount }, TestLongWait, TestWaitTick)
		assert.Eventually(t, func() bool { reg.waitgroup.Wait(); return true }, TestLongWait, TestWaitTick)
		assert.False(t, rxDone)
		close(testCh)
		assert.Eventually(t, func() bool { return rxDone }, 250*time.Millisecond, TestWaitTick)
		assert.Equal(t, channelCount, rxCount)
		assert.Len(t, reg.pending, 0)

		assert.Equal(t, Stats{Misses: channelCount}, reg.Stats)
	})
}

func TestRegistry_register(t *testing.T) {
	t.Parallel()
	t.Run("exists", func(t *testing.T) {
		t.Parallel()
		const key, value = "piggie", "laundry"
		reg := NewRegistry()
		reg.registry[key] = value
		got := reg.register(key, value)
		assert.Equal(t, value, got)
		assert.Len(t, reg.registry, 1) // only one entry still
		assert.Empty(t, reg.lookups)   // no lookups created

		assert.Equal(t, Stats{Duplicates: 1}, reg.Stats)
	})

	t.Run("insertion", func(t *testing.T) {
		t.Parallel()
		const key1, value1 = "french", "toast"
		reg := NewRegistry()
		t.Run("first", func(t *testing.T) {
			got := reg.register(key1, value1)
			// it should tell us that it wasn't registered.
			assert.Equal(t, unregisteredValue, got)
			assert.Len(t, reg.registry, 1) // only one entry
			assert.Equal(t, value1, reg.registry[key1])
			assert.Empty(t, reg.lookups) // no lookups
			assert.Equal(t, Stats{Registrations: 1}, reg.Stats)
		})

		const key2, value2 = "wet", "dog"
		t.Run("second", func(t *testing.T) {
			got := reg.register(key2, value2)
			assert.Equal(t, unregisteredValue, got)
			assert.Len(t, reg.registry, 2)
			assert.Equal(t, value2, reg.registry[key2])
			assert.Empty(t, reg.lookups)                // no lookups
			assert.Equal(t, value1, reg.registry[key1]) // no-clobber check
			assert.Equal(t, Stats{Registrations: 2}, reg.Stats)
		})
	})

	t.Run("resolution", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		// create three channels to ensure we notify the correct ones.
		ch1, ch2, ch3 := make(chan interface{}), make(chan interface{}), make(chan interface{})
		defer close(ch1)
		defer close(ch2)
		defer close(ch3)
		reg.pending["ermin"] = []chan<- interface{}{ch1}
		reg.pending["zeb"] = []chan<- interface{}{ch2}
		reg.pending["dou"] = []chan<- interface{}{ch3}
		var rx1, rx2, rx3 interface{} = nil, nil, nil
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			rx1 = <-ch1
		}()
		go func() {
			defer wg.Done()
			rx2 = <-ch2
		}()
		go func() {
			rx3 = <-ch3
		}()
		reg.register("ermin", "trude")
		reg.register("zeb", "edee")
		assert.Equal(t, Stats{Registrations: 2, Resolved: 2}, reg.Stats)

		assert.Contains(t, reg.registry, "ermin")
		assert.Contains(t, reg.registry, "zeb")
		assert.Eventually(t, func() bool { wg.Wait(); return true }, TestLongWait, TestWaitTick)
		assert.Equal(t, "trude", rx1)
		assert.Equal(t, "edee", rx2)
		assert.Never(t, func() bool { return rx3 != nil }, TestLongWait, TestWaitTick)

		assert.Len(t, reg.pending, 1)
		assert.Contains(t, reg.pending, "dou")

		// let channel 3 go
		reg.closePending()

		assert.Eventually(t, func() bool { reg.waitgroup.Wait(); return true }, TestLongWait, TestWaitTick)

		assert.Equal(t, Stats{Registrations: 2, Resolved: 2, Misses: 1}, reg.Stats)
	})
}

// lookup helper.
func testLookup(reg *Registry, key string) (wg *sync.WaitGroup, ch chan interface{}) {
	wg = new(sync.WaitGroup)
	// 1-buffer so that the manager doesn't block.
	ch = make(chan interface{}, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		reg.lookup(key, ch)
	}()
	return
}

func TestRegistry_lookup(t *testing.T) {
	t.Parallel()
	t.Run("non-blocking", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.registry["fu"] = "bar"
		wg, responseCh := testLookup(reg, "fu")
		assert.Eventually(t, func() bool { return <-responseCh == "bar" }, TestLongWait, TestWaitTick)
		testWaitGroup(t, wg, true, TestLongWait)
		assert.Equal(t, Stats{Hits: 1}, reg.Stats)
	})

	t.Run("blocking", func(t *testing.T) {
		t.Parallel()
		t.Run("unresolved", func(t *testing.T) {
			t.Parallel()
			reg := NewRegistry()
			wg, responseCh := testLookup(reg, "fish")
			testWaitGroup(t, wg, true, TestShortWait)
			// lookup should return almost instantly, by deferring us.
			assert.Contains(t, reg.pending, "fish")
			assert.Equal(t, Stats{Defers: 1}, reg.Stats)

			reg.closePending()

			testWaitGroup(t, wg, true, TestLongWait)
			assert.Equal(t, unregisteredValue, <-responseCh)
			assert.Equal(t, Stats{Defers: 1, Misses: 1}, reg.Stats)
		})
		t.Run("resolved", func(t *testing.T) {
			t.Parallel()
			reg := NewRegistry()
			wg, responseCh := testLookup(reg, "rand")
			// lookup should return almost instantly, by deferring us.
			testWaitGroup(t, wg, true, TestShortWait)
			assert.Contains(t, reg.pending, "rand")
			assert.Equal(t, Stats{Defers: 1}, reg.Stats)

			reg.register("rand", "al'thor")

			testWaitGroup(t, wg, true, TestLongWait)
			assert.Equal(t, "al'thor", <-responseCh)
			assert.Len(t, reg.pending, 0)
			reg.register("rand", "al'thor")
			assert.Equal(t, Stats{Defers: 1, Resolved: 1, Registrations: 1, Duplicates: 1}, reg.Stats)
		})
	})
}

func newRunningManager(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	reg.waitgroup.Add(1)
	go reg.manager()
	testWaitGroup(t, &reg.waitgroup, false, TestLongWait)
	return reg
}

type mockRegistry struct {
	*Registry
	wg         sync.WaitGroup
	responseCh chan interface{}
	response   interface{}
}

func newMockRegistry(t *testing.T, data map[string]interface{}) *mockRegistry {
	t.Helper()
	manager := &mockRegistry{
		Registry:   newRunningManager(t),
		wg:         sync.WaitGroup{},
		responseCh: make(chan interface{}),
		response:   nil,
	}
	manager.registry = data
	return manager
}

func TestRegistry_manager(t *testing.T) {
	t.Run("control", func(t *testing.T) {
		reg := newRunningManager(t)
		assert.NotNil(t, reg.pending)
		assert.Len(t, reg.pending, 0)
		assert.Len(t, reg.registry, 0)
		close(reg.registrations)
		testWaitGroup(t, &reg.waitgroup, true, TestLongWait)
		assert.Len(t, reg.registry, 0)
		assert.Equal(t, Stats{}, reg.Stats)
		assert.Nil(t, reg.lookups)
	})
	t.Run("operations", func(t *testing.T) {
		t.Run("lookup", func(t *testing.T) {
			t.Parallel()
			t.Run("immediate", func(t *testing.T) {
				t.Parallel()
				test := newMockRegistry(t, map[string]interface{}{
					"foo": "nothing", "fu": "bar", "bar": "fizz",
				})
				testTimed(t, true,
					func() { test.response = <-test.responseCh },
					func() { test.lookups <- &lookup{key: "fu", response: test.responseCh} })
				// should happen faster on most machines :)
				assert.Equal(t, test.registry["fu"], test.response)
				assert.Len(t, test.pending, 0)
				assert.Equal(t, Stats{Hits: 1}, test.Stats)
			})
			t.Run("deferred", func(t *testing.T) {
				t.Parallel()
				test := newMockRegistry(t, map[string]interface{}{
					"a": "A", "b": "B", "d": "D",
				})
				var lookupResp interface{}
				lookupCh := make(chan interface{})
				defer close(lookupCh)

				t.Run("lookup-defers", func(t *testing.T) {
					testTimed(t, true, func() { test.lookups <- &lookup{key: "c", response: lookupCh} })
					assert.Eventually(t, func() bool { return len(test.pending) == 1 }, TestShortWait, TestWaitTick)
					assert.Contains(t, test.pending, "c")
					assert.Equal(t, Stats{Defers: 1}, test.Stats)
				})

				t.Run("register-triggers", func(t *testing.T) {
					testTimed(t, true,
						func() { lookupResp = <-lookupCh },
						func() {
							_, _, err := test.Register("c", "C")
							assert.Nil(t, err)
						},
					)
				})

				assert.Equal(t, "C", lookupResp)
				assert.Equal(t, Stats{Defers: 1, Registrations: 1, Resolved: 1}, test.Stats)
			})
		})
	})
}

func TestRegistry_Start(t *testing.T) {
	t.Parallel()
	t.Run("nominal", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.Start()
		// sending a lookup should cause a deferral.
		testCh := make(chan interface{}, 1)
		testTimed(t, true, func() {
			reg.lookups <- &lookup{"bottle", testCh}
		})
		// but we shouldn't get a result
		testTimed(t, false, func() {
			<-testCh
			fmt.Println("result acquired")
		})
		testTimed(t, true, reg.Stop)

		// and verify that stopping the manager shut down the lookups
		assert.Eventually(t, func() bool { return reg.lookups == nil }, TestShortWait, TestWaitTick)
	})

	t.Run("restart", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.Stop()
		require.Nil(t, reg.registrations)
		reg.Start()
		assert.NotNil(t, reg.registrations)
		testTimed(t, true, func() { reg.Register("french", "fries") })
		reg.Stop()
		assert.Contains(t, reg.registry, "french")
	})
}

func TestRegistry_Stop(t *testing.T) {
	t.Parallel()
	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		reg := &Registry{}
		reg.Stop()
	})
	t.Run("immediately", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		testTimed(t, true, reg.Stop)
	})
	t.Run("on Done", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.waitgroup.Add(2)
		testTimed(t, true, reg.Stop,
			reg.waitgroup.Done,
			reg.waitgroup.Done)
	})
	t.Run("channel closures", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.Stop()
		assert.Nil(t, reg.registrations)
		// but it shouldn't have touched the lookup channel, manager does that.
		assert.NotNil(t, reg.lookups)
	})
}

func TestRegistry_Values(t *testing.T) {
	reg := NewRegistry()
	assert.NotNil(t, reg.pending)
	t.Run("running", func(t *testing.T) {
		val, err := reg.Values()
		if assert.Error(t, err) {
			assert.ErrorIs(t, err, ErrRunning)
			assert.Nil(t, val)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		reg.pending = nil
		val, err := reg.Values()
		if assert.Nil(t, err) {
			assert.Equal(t, reg.registry, val)
		}
	})
}

func TestRegistry_Register(t *testing.T) {
	t.Parallel()
	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		close(reg.registrations)
		assert.Panics(t, func() {
			_, _, err := reg.Register("biscuit", nil)
			if err != nil {
				t.Fatalf("register should have panicked, got an error instead.")
			}
		})
	})
	t.Run("gets closed", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		var value interface{}
		var ok bool
		var err error
		// try to register (which will block, because, no manager),
		// and then close the channel underneath it.
		testTimed(t, true,
			func() {
				value, ok, err = reg.Register("xyz", "abc")
			},
			func() {
				request := <-reg.registrations
				close(request.response)
			})

		if assert.ErrorIs(t, err, ErrClosed) {
			assert.Nil(t, value)
			assert.False(t, ok)
		}
	})

	t.Run("unregistered", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.Start()
		testTimed(t, true,
			func() {
				registered, ok, err := reg.Register("cookies", "cream")
				if assert.True(t, ok) {
					assert.Nil(t, err)
					// Unlike the internal implementation, the API version
					// returns the registered value either way.
					assert.Equal(t, "cream", registered)
				}
			})
		if assert.Contains(t, reg.registry, "cookies") {
			assert.Equal(t, Stats{Registrations: 1}, reg.Stats)
		}
	})

	t.Run("collision", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.registry["time"] = "wibbly"
		reg.Start()
		testTimed(t, true,
			func() {
				registered, ok, err := reg.Register("time", "wobbly")
				if assert.False(t, ok) {
					assert.Nil(t, err)
					assert.Equal(t, "wibbly", registered)
				}
			})
		assert.Equal(t, Stats{Duplicates: 1}, reg.Stats)
	})
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	t.Run("gets closed", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		var value interface{}
		var ok bool
		var err error
		// try to register (which will block, because, no manager),
		// and then close the channel underneath it.
		testTimed(t, true,
			func() {
				value, ok, err = reg.Lookup("xyz")
			},
			func() {
				request := <-reg.lookups
				close(request.response)
			})

		if assert.ErrorIs(t, err, ErrClosed) {
			assert.Nil(t, value)
			assert.False(t, ok)
		}
	})

	t.Run("after close", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.registry["fu"] = "bar"
		reg.Stop()
		var value interface{}
		var ok = true
		var err error
		if assert.Eventually(t, func() bool { value, ok, err = reg.Lookup("fizz"); return true }, TestShortWait, TestWaitTick) {
			assert.Nil(t, err)
			assert.Nil(t, value)
			assert.False(t, ok)
		}
	})

	t.Run("nominal", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		reg.registry["man"] = "plan"
		reg.Start() // need a manager to respond for us

		t.Run("hit", func(t *testing.T) {
			var value interface{}
			var ok bool
			var err error

			testTimed(t, true,
				func() {
					value, ok, err = reg.Lookup("man")
				})
			if assert.True(t, ok) {
				assert.Equal(t, "plan", value)
				assert.Nil(t, err)
			}
			assert.Equal(t, Stats{Hits: 1}, reg.Stats)
		})

		t.Run("defer-unresolved", func(t *testing.T) {
			var value interface{} = unregisteredValue
			var ok bool
			var err error

			testTimed(t, true,
				func() {
					value, ok, err = reg.Lookup("plan")
				},
				func() {
					assert.Contains(t, reg.pending, "plan")
				},
				func() {
					reg.Stop()
				})
			assert.False(t, ok)
			assert.Nil(t, value)
			assert.Nil(t, err)
		})
	})
}

func TestRegistry_OnDefer(t *testing.T) {
	t.Parallel()
	t.Run("sets", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		var invoked bool
		reg.OnDefer(func() { invoked = true })
		assert.NotNil(t, reg.onDefer)
		reg.onDefer()
		assert.True(t, invoked)
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		var invoked bool
		var wg sync.WaitGroup

		reg := NewRegistry()
		reg.OnDefer(func() { invoked = true })
		require.NotNil(t, reg.onDefer)

		reg.Start()
		defer reg.Stop()

		// invoked shouldn't get changed with a registered lookup.
		testTimed(t, true, func() { reg.Register("barney", 0) })
		testTimed(t, true, func() { reg.Lookup("barney") })
		assert.Never(t, func() bool { return invoked }, TestShortWait, TestWaitTick)

		// park a deferred lookup
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.Lookup("fred")
		}()

		testTimed(t, true, func() { reg.Register("fred", nil) })
		assert.Eventually(t, func() bool { return invoked }, TestLongWait, TestWaitTick)
		assert.Eventually(t, func() bool { wg.Wait(); return true }, TestShortWait, TestWaitTick)
	})
}

func TestRegistry_OnResolve(t *testing.T) {
	t.Parallel()
	t.Run("sets", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		var count int
		reg.OnResolve(func(i int) {
			count = i
		})
		assert.NotNil(t, reg.onResolve)
		reg.onResolve(7)
		assert.Equal(t, 7, count)
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		var count int

		reg := NewRegistry()
		reg.Start()
		defer reg.Stop()

		reg.OnResolve(func(i int) {
			count = i
		})

		// count shouldn't get changed with a registered lookup.
		testTimed(t, true, func() { reg.Register("fred", 0) })
		testTimed(t, true, func() { reg.Lookup("fred") })
		assert.Never(t, func() bool { return count > 0 }, TestShortWait, TestWaitTick)

		wg.Add(2)
		go func() {
			defer wg.Done()
			reg.Lookup("barney")
		}()
		go func() {
			defer wg.Done()
			reg.Lookup("barney")
		}()

		testWaitGroup(t, &wg, false, TestShortWait)
		testTimed(t, true, func() { reg.Register("barney", nil) })
		assert.Eventually(t, func() bool { return count == 2 }, TestShortWait, TestWaitTick)
		testTimed(t, true, func() { reg.Register("fred", nil) })
		assert.Never(t, func() bool { return count > 2 }, TestShortWait, TestWaitTick)

		testTimed(t, true, wg.Wait)
	})
}
