package worker

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"plugin"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const pluginsDir = "/var/tmp/polytank"

type runFunction func(index, timeout uint32, ch chan []string, exMap map[string]string) error

type counter uint32

func (c *counter) Increment() uint32 {
	return atomic.AddUint32((*uint32)(c), 1)
}
func (c *counter) Load() uint32 {
	return atomic.LoadUint32((*uint32)(c))
}

type Service struct {
	resultCh chan []string
	cancelCh chan struct{}
	runFunc  runFunction
	counter  counter
}

func NewService() pb.WorkerServer {
	var c counter
	return &Service{
		runFunc: nil,
		counter: c,
	}
}

func (s *Service) Distribute(ctx context.Context, req *pb.DistributeRequest) (*empty.Empty, error) {
	if err := s.storePlugins(req.Plugin); err != nil {
		return nil, err
	}
	p, err := plugin.Open(filepath.Join(pluginsDir, "plugin.so"))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot open plugins: %+v", err)
	}
	symbol, err := p.Lookup("Run")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot create symbol from plugins")
	}
	runFunc, ok := symbol.(func(index, timeout uint32, ch chan []string, exMap map[string]string) error)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "cannot assertion symbol to runFunction")
	}
	s.runFunc = runFunc
	return &empty.Empty{}, nil
}

func (s *Service) storePlugins(b []byte) error {
	st := status.New(codes.InvalidArgument, "cannot store plugins")

	if err := os.MkdirAll(pluginsDir, 0777); err != nil {
		return st.Err()
	}
	f, err := os.Create(filepath.Join(pluginsDir, "plugin.so"))
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

func (s *Service) Execute(ctx context.Context, req *pb.ExecuteRequest) (*empty.Empty, error) {
	if s.runFunc == nil {
		return nil, status.Error(codes.InvalidArgument, "please distribute first")
	}

	s.resultCh = make(chan []string, 10000)
	s.cancelCh = make(chan struct{}, 1)

	maxCount := req.GetCount()
	maxSeconds := req.GetSeconds()

	if maxSeconds > 0 {
		go s.threadForTimeout(req, maxSeconds)
	} else {
		go s.threadForCount(req, maxCount)
	}

	return &empty.Empty{}, nil
}

func (s *Service) threadForTimeout(req *pb.ExecuteRequest, maxSeconds uint32) {
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
				s.goroutineMethod(req, concurrentCh)
			}()
		}
	}

	wg.Wait()
	close(s.resultCh)
}

func (s *Service) threadForCount(req *pb.ExecuteRequest, maxCount uint32) {
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
				s.goroutineMethod(req, concurrentCh)
			}()
		}
	}

	wg.Wait()
	close(s.resultCh)
}

func (s *Service) goroutineMethod(req *pb.ExecuteRequest, ch chan struct{}) {
	defer func() {
		<-ch
	}()
	c := s.counter.Increment()
	if err := s.runFunc(c, req.TimeoutSeconds, s.resultCh, req.ExMap); err != nil {
		log.Printf("runFunc error: %+v", err)
	}
}

func (s *Service) sliceToResult(slice []string) (*pb.Result, error) {
	if len(slice) != 4 {
		return nil, status.Error(codes.InvalidArgument, "cannot parse result")
	}

	startNanoSec, err := strconv.ParseInt(slice[2], 10, 64)
	if err != nil {
		return nil, err
	}
	endNanoSec, err := strconv.ParseInt(slice[3], 10, 64)
	if err != nil {
		return nil, err
	}
	elapsed := endNanoSec - startNanoSec
	return &pb.Result{
		ProcessName:        slice[0],
		IsSuccess:          slice[1] == "true",
		StartTimestampUsec: startNanoSec,
		ElapsedTimeUsec:    elapsed,
	}, nil
}

func (s *Service) Wait(_ *empty.Empty, server pb.Worker_WaitServer) error {
	ticker := time.NewTicker(1 * time.Second)

	var results []*pb.Result
	sendErr := status.New(codes.Internal, "send results error").Err()
ForLoop:
	for {
		select {
		case <-ticker.C:
			if len(results) == 0 {
				continue
			}
			if err := server.Send(&pb.WaitResponse{
				Results:    results,
				IsContinue: true,
				Current:    s.counter.Load(),
			}); err != nil {
				return sendErr
			}
			results = []*pb.Result{}
		case slice, ok := <-s.resultCh:
			if !ok {
				break ForLoop
			}
			res, err := s.sliceToResult(slice)
			if err != nil {
				log.Printf("result convert err: %+v", err)
				continue
			}
			results = append(results, res)
		}
	}

	if err := server.Send(&pb.WaitResponse{
		Results:    results,
		IsContinue: false,
		Current:    s.counter.Load(),
	}); err != nil {
		return sendErr
	}
	return nil
}

func (s *Service) Stop(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	s.cancelCh <- struct{}{}
	close(s.resultCh)
	close(s.cancelCh)
	return &empty.Empty{}, nil
}
