package worker

import (
	"context"
	"log"
	"os"
	"plugin"
	"sync"
	"time"

	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const pluginsPath = "/var/tmp/polytank/plugins.so"

type Service struct {
	resultCh chan *pb.Result
	cancelCh chan struct{}
	runFunc  RunFunction
}

type RunFunction func(timeout uint32, ch chan *pb.Result) error

func NewService() pb.WorkerServer {
	return &Service{
		resultCh: make(chan *pb.Result, 10000),
		cancelCh: make(chan struct{}, 1),
		runFunc:  nil,
	}
}

func (s Service) Distribute(ctx context.Context, req *pb.DistributeRequest) (*pb.DistributeResponse, error) {
	if err := s.storePlugins(req.Plugin); err != nil {
		return nil, err
	}
	p, err := plugin.Open(pluginsPath)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot open plugins")
	}
	symbol, err := p.Lookup("Run")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot create symbol from plugins")
	}
	runFunc, ok := symbol.(RunFunction)
	if !ok {
		return nil, status.New(codes.InvalidArgument, "cannot assertion symbol to RunFunction").Err()
	}
	s.runFunc = runFunc
	return &pb.DistributeResponse{}, nil
}

func (s Service) storePlugins(b []byte) error {
	f, err := os.Create(pluginsPath)
	st := status.New(codes.InvalidArgument, "cannot store plugins")
	if err != nil {
		return st.Err()
	}
	if _, err := f.Write(b); err != nil {
		return st.Err()
	}
	if err := f.Close(); err != nil {
		return st.Err()
	}
	return nil
}

func (s Service) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	if s.runFunc == nil {
		return nil, status.New(codes.InvalidArgument, "please distribute first").Err()
	}

	maxCount := req.GetCount()
	maxSeconds := req.GetSeconds()

	if maxSeconds > 0 {
		go s.threadForTimeout(req, maxSeconds)
	} else {
		go s.threadForCount(req, maxCount)
	}

	return &pb.ExecuteResponse{}, nil
}

func (s Service) threadForTimeout(req *pb.ExecuteRequest, maxSeconds uint32) {
	wg := &sync.WaitGroup{}
	concurrentCh := make(chan struct{}, req.Concurrency)
	timeoutCh := time.After(time.Duration(maxSeconds) * time.Second)

ForLoop:
	for {
		select {
		case <-timeoutCh:
			break ForLoop
		case <-s.cancelCh:
			break ForLoop
		default:
			concurrentCh <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.goroutineMethod(req.TimeoutSeconds, concurrentCh)
			}()
		}
	}

	wg.Wait()
	close(s.resultCh)
}

func (s Service) threadForCount(req *pb.ExecuteRequest, maxCount uint32) {
	wg := &sync.WaitGroup{}
	concurrentCh := make(chan struct{}, req.Concurrency)

ForLoop:
	for i := uint32(0); i < maxCount; i++ {
		select {
		case <-s.cancelCh:
			break ForLoop
		default:
			concurrentCh <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.goroutineMethod(req.TimeoutSeconds, concurrentCh)
			}()
		}
	}

	wg.Wait()
	close(s.resultCh)
}

func (s Service) goroutineMethod(timeoutSec uint32, ch chan struct{}) {
	defer func() {
		<-ch
	}()
	if err := s.runFunc(timeoutSec, s.resultCh); err != nil {
		log.Printf("runFunc error: %+v", err)
	}
}

func (s Service) Wait(req *pb.WaitRequest, server pb.Worker_WaitServer) error {
	ticker := time.NewTicker(1 * time.Second)

	var results []*pb.Result
	sendErr := status.New(codes.Internal, "send results error").Err()
ForLoop:
	for {
		select {
		case <-ticker.C:
			if err := server.Send(&pb.WaitResponse{
				Results:    results,
				IsContinue: true,
			}); err != nil {
				return sendErr
			}
			results = []*pb.Result{}
		case res, ok := <-s.resultCh:
			if !ok {
				break ForLoop
			}
			results = append(results, res)
		}
	}

	if err := server.Send(&pb.WaitResponse{
		Results:    results,
		IsContinue: false,
	}); err != nil {
		return sendErr
	}
	return nil
}

func (s Service) Stop(context.Context, *pb.StopRequest) (*pb.StopResponse, error) {
	s.cancelCh <- struct{}{}
	close(s.resultCh)
	return &pb.StopResponse{}, nil
}
