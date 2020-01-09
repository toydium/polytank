package worker

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/hashicorp/go-hclog"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc/metadata"
)

type waitServerMock struct {
	mtx         *sync.Mutex
	sendResults []*pb.WaitResponse
}

func (w *waitServerMock) Send(r *pb.WaitResponse) error {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.sendResults = append(w.sendResults, r)
	return nil
}
func (w *waitServerMock) SetHeader(metadata.MD) error  { return nil }
func (w *waitServerMock) SendHeader(metadata.MD) error { return nil }
func (w *waitServerMock) SetTrailer(metadata.MD)       {}
func (w *waitServerMock) Context() context.Context     { return nil }
func (w *waitServerMock) SendMsg(m interface{}) error  { return nil }
func (w *waitServerMock) RecvMsg(m interface{}) error  { return nil }

func newWaitServerMock() *waitServerMock {
	return &waitServerMock{
		sendResults: []*pb.WaitResponse{},
		mtx:         &sync.Mutex{},
	}
}

func TestCounter(t *testing.T) {
	c := counter(0)
	wg := &sync.WaitGroup{}
	t.Run("increment", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.Increment()
			}()
		}
	})
	wg.Wait()
	if c.Load() != 100 {
		t.Fail()
	}
}

func TestService(t *testing.T) {
	s := &Service{
		counter: counter(0),
		logger: hclog.New(&hclog.LoggerOptions{
			Name:       "polytank-worker-test",
			Level:      hclog.Info,
			Output:     os.Stdout,
			JSONFormat: true,
		}),
		waitTickerMSec: 10,
	}
	defer s.DisconnectPlugin()

	ctx := context.TODO()
	runnerName := "ex_runner"
	if runtime.GOOS == "darwin" {
		runnerName += "_darwin"
	}
	b, err := ioutil.ReadFile(filepath.Join(os.Getenv("ROOT_PATH"), "example", runnerName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Distribute(ctx, &pb.DistributeRequest{Plugin: b}); err != nil {
		t.Fatal(err)
	}

	t.Run("execute", func(t *testing.T) {
		if _, err := s.Execute(ctx, &pb.ExecuteRequest{
			Concurrency:    5,
			TimeoutSeconds: 1,
			Max:            &pb.ExecuteRequest_Count{Count: 10},
			ExMap:          nil,
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wait", func(t *testing.T) {
		mock := newWaitServerMock()
		if err := s.Wait(&empty.Empty{}, mock); err != nil {
			t.Fatal(err)
		}
		if len(mock.sendResults) == 0 {
			t.Fail()
		}
		c := s.counter.Load()
		if c == 0 {
			t.Fail()
		}
	})

	t.Run("stop", func(t *testing.T) {
		if _, err := s.Execute(ctx, &pb.ExecuteRequest{
			Concurrency:    2,
			TimeoutSeconds: 1,
			Max:            &pb.ExecuteRequest_Seconds{Seconds: 1},
			ExMap:          nil,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Stop(ctx, &empty.Empty{}); err != nil {
			t.Fatal(err)
		}
		if s.counter.Load() > 0 {
			t.Fail()
		}
	})
}
