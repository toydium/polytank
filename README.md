# polytank
load test tool by gRPC and Go

# install
```
go get -u github.com/toydium/polytank/cmd/polytank
go get -u github.com/toydium/polytank/cmd/polytank-cli
```

# Usage
## build plugins
1. implement plugin code
2. build binary

### plugin code example
```go
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
```

## running by standalone mode
`polytank -mode standalone -config ./config.yml -plugin ./plugin -port 33333`

### config yaml example
```yaml
concurrency: 4
timeout: 5
# set seconds of execution or count of execution
max_seconds: 10
#max_count: 1000
ex_map:
  host: localhost
  port: 8080
```

## running by controlled mode
### launch worker
```
$ polytank -mode worker -port 33333
```

### launch controller
```
$ polytank -mode controller -port 33334
```

### control by cli
```
# add worker server to controller
polytank-cli -addr localhost:33334 add-worker localhost:33333

# set plugin and execute configutation
polytank-cli -addr localhost:33334 set-plugin ./plugin
polytank-cli -addr localhost:33334 set-execute-request '{"concurrency":4,"timeoutSeconds":5,"seconds":10,"exMap":{"host":"front-envoy-blue","port":"8001"}}'

# start execition and wait for result
polytank-cli -addr localhost:33334 start

# abort all worker execution
polytank-cli -addr localhost:33334 stop
```
