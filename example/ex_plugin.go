package main

import (
	"fmt"
	"math/rand"
	"time"
)

func Run(index, timeout uint32, ch chan []string, exMap map[string]string) error {
	rand.Seed(time.Now().UnixNano())

	for i := 0; i < 5; i++ {
		res := childRun(fmt.Sprintf("process_%d_%d", index, i+1))
		ch <- res
	}

	return nil
}

func childRun(name string) []string {
	s := time.Now()

	// some process
	r := rand.Int31n(10)
	time.Sleep(time.Duration(r+1) * time.Millisecond)

	e := time.Now()

	return []string{
		// result name
		name,
		// success or failure
		"true",
		// start nano seconds
		fmt.Sprint(s.UnixNano()),
		// end nano seconds
		fmt.Sprint(e.UnixNano()),
	}
}
