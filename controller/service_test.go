package controller

import (
	"context"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type waitServerMock struct {
	mtx     *sync.Mutex
	results []*pb.ControllerWaitResponse
}

func (m *waitServerMock) Send(res *pb.ControllerWaitResponse) error {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.results = append(m.results, res)
	return nil
}
func (m *waitServerMock) SetHeader(metadata.MD) error  { return nil }
func (m *waitServerMock) SendHeader(metadata.MD) error { return nil }
func (m *waitServerMock) SetTrailer(metadata.MD)       {}
func (m *waitServerMock) Context() context.Context     { return nil }
func (m *waitServerMock) SendMsg(i interface{}) error  { return nil }
func (m *waitServerMock) RecvMsg(i interface{}) error  { return nil }

func newWaitServerMock() *waitServerMock {
	return &waitServerMock{
		mtx:     &sync.Mutex{},
		results: []*pb.ControllerWaitResponse{},
	}
}

func TestService(t *testing.T) {
	w := worker.NewService(10)
	defer w.DisconnectPlugin()

	workerAddr := "127.0.0.1:39999"
	l, err := net.Listen("tcp", workerAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	gs := grpc.NewServer(grpc.MaxRecvMsgSize(1024 * 1024 * 100))
	pb.RegisterWorkerServer(gs, w)
	defer gs.GracefulStop()

	go func() {
		if err := gs.Serve(l); err != nil {
			t.Logf("close worker: %+v", err)
		}
	}()

	s := NewService()
	ctx := context.TODO()
	t.Run("add_worker", func(t *testing.T) {
		req := &pb.AddWorkerRequest{
			Addresses: []string{workerAddr},
		}
		res, err := s.AddWorker(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		workers := res.Workers
		if len(workers) != 1 {
			t.Fail()
		}
		w := workers[0]
		if w.Address != workerAddr {
			t.Fail()
		}
		if w.Status != pb.WorkerInfo_INITIALIZED {
			t.Fail()
		}
	})

	var uuid string
	t.Run("status", func(t *testing.T) {
		res, err := s.Status(ctx, &empty.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Workers) != 1 {
			t.Fail()
		}
		workers := res.Workers
		if len(workers) != 1 {
			t.Fail()
		}
		w := workers[0]
		if w.Address != workerAddr {
			t.Fail()
		}
		if w.Status != pb.WorkerInfo_INITIALIZED {
			t.Fail()
		}
		uuid = w.Uuid
	})

	t.Run("set_plugin", func(t *testing.T) {
		runnerName := "ex_runner"
		if runtime.GOOS == "darwin" {
			runnerName += "_darwin"
		}
		b, err := ioutil.ReadFile(filepath.Join(os.Getenv("ROOT_PATH"), "example", runnerName))
		if err != nil {
			t.Fatal(err)
		}

		res, err := s.SetPlugin(ctx, &pb.DistributeRequest{Plugin: b})
		if err != nil {
			t.Fatal(err)
		}
		w := res.Workers[0]
		if w.Status != pb.WorkerInfo_DISTRIBUTED {
			t.Fail()
		}
	})

	t.Run("set_execute_request", func(t *testing.T) {
		req := &pb.ExecuteRequest{
			Concurrency:    2,
			TimeoutSeconds: 1,
			Max:            &pb.ExecuteRequest_Count{Count: 10},
		}
		if _, err := s.SetExecuteRequest(ctx, req); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("start", func(t *testing.T) {
		req := &pb.StartRequest{
			Uuids: []string{uuid},
		}
		res, err := s.Start(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		w := res.Workers[0]
		if w.Status != pb.WorkerInfo_RUNNING {
			t.Fail()
		}
	})

	t.Run("wait", func(t *testing.T) {
		mock := newWaitServerMock()
		if err := s.Wait(&pb.ControllerWaitRequest{Uuid: uuid}, mock); err != nil {
			t.Fatal(err)
		}
		if len(mock.results) == 0 {
			t.Fail()
		}
		count := 0
		for _, r := range mock.results {
			count += len(r.WaitResponse.Results)
		}
		if count != 50 {
			t.Fail()
		}
	})

	t.Run("stop", func(t *testing.T) {
		req := &pb.ExecuteRequest{
			Concurrency:    1,
			TimeoutSeconds: 1,
			Max:            &pb.ExecuteRequest_Seconds{Seconds: 5},
		}
		if _, err := s.SetExecuteRequest(ctx, req); err != nil {
			t.Fatal(err)
		}

		startReq := &pb.StartRequest{
			Uuids: []string{uuid},
		}
		if _, err := s.Start(ctx, startReq); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)

		if _, err := s.Stop(ctx, &empty.Empty{}); err != nil {
			t.Fatal(err)
		}

		mock := newWaitServerMock()
		if err := s.Wait(&pb.ControllerWaitRequest{Uuid: uuid}, mock); err != nil {
			t.Fatal(err)
		}

		l := len(mock.results)
		if l == 0 || l > 5 {
			t.Fail()
		}

		res, err := s.Status(ctx, &empty.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		w := res.Workers[0]
		if w.Status != pb.WorkerInfo_ABORTED {
			t.Fail()
		}
	})
}
