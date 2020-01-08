package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/runner"
)

type ExRunnerPlugin struct {
}

// implement method adopt to runner.Runner interface
func (p *ExRunnerPlugin) Run(index, timeout uint32, exMap map[string]string) (res []*pb.Result, err error) {
	rand.Seed(time.Now().UnixNano())

	for i := 0; i < 5; i++ {
		r := p.child(fmt.Sprintf("process_%d_%d", index, i))
		res = append(res, r)
	}
	return res, nil
}

func (p *ExRunnerPlugin) child(name string) *pb.Result {
	s := time.Now()

	// some process
	r := rand.Int31n(10)
	time.Sleep(time.Duration(r+1) * time.Millisecond)

	e := time.Now()

	return &pb.Result{
		ProcessName:        name,
		IsSuccess:          true,
		StartTimestampUsec: s.UnixNano(),
		ElapsedTimeUsec:    e.UnixNano() - s.UnixNano(),
	}
}

// call runner.Serve() with custom struct
func main() {
	runner.Serve(&ExRunnerPlugin{})
}
