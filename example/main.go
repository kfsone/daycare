package main

import (
	"fmt"
	"time"

	"github.com/kfsone/daycare"
)

func main() {
	r := daycare.NewRegistry()

	r.Start()

	var solution interface{}

	// put something into the registry.
	r.Register("distraction", "squirrel")

	// A worker trying to look up 'answer'.
	go func() {
		answer, valid, err := r.Lookup("answer")
		if err != nil {
			panic(err)
		}
		if !valid {
			panic("no response")
		}
		fmt.Println("got the answer")
		solution = answer
	}()

	// A worker looking for something we'll never register.
	go func() {
		_, valid, err := r.Lookup("suggestions")
		if err != nil {
			panic(err)
		}
		if !valid {
			fmt.Println("suggestions stayed empty, like we wanted.")
		} else {
			panic("there's a suggestion in the box.")
		}
	}()

	// A worker that will register 'question' after a few moments.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		r.Register("question", func() int { return 6 * 7 })
	}()

	// A worker that will register "answer" after more moments.
	go func() {
		time.Sleep(2500 * time.Millisecond)
		r.Register("answer", 42)
	}()

	// Time passes...
	for i := 0; i < 3; i++ {
		time.Sleep(1 * time.Second)
		fmt.Printf("Answer? %v\n", solution)
	}

	r.Stop()

	// Allow our blocked worker to get their notification.
	time.Sleep(100 * time.Millisecond)

	registered, err := r.GetData()
	if err != nil {
		panic(err)
	}
	fmt.Printf("registry: %#+v\n", registered)
}
