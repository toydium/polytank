# polytank
load test tool by gRPC and Go

# install
```
go get -u github.com/toydium/polytank/cmd/polytank
go get -u github.com/toydium/polytank/cmd/polytank-cli
```

# Usage
## build plugins
1. implements plugin code
2. build by `-buildmode=plugin`

### plugin code example
```go
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
```

## running by standalone mode
`polytank -mode standalone -config config.yml -plugin plugin.so -port 33333`

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
polytank-cli -addr localhost:33334 set-plugin plugin.so
polytank-cli -addr localhost:33334 set-execute-request '{"concurrency":4,"timeoutSeconds":5,"seconds":10,"exMap":{"host":"front-envoy-blue","port":"8001"}}'

# start execition and wait for result
polytank-cli -addr localhost:33334 start (worker-uuid-1) (worker-uuid-2) ...
polytank-cli -addr localhost:33334 wait (worker-uuid)

# abort all worker execution
polytank-cli -addr localhost:33334 stop
```
