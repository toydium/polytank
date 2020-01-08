package worker

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/hashicorp/go-plugin"
	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/runner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const pluginsDir = "/var/tmp/polytank"

type counter uint32

func (c *counter) Increment() uint32 {
	return atomic.AddUint32((*uint32)(c), 1)
}
func (c *counter) Load() uint32 {
	return atomic.LoadUint32((*uint32)(c))
}
func (c *counter) Reset() {
	atomic.StoreUint32((*uint32)(c), 0)
}

type Service struct {
	resultCh     chan []*pb.Result
	cancelCh     chan struct{}
	counter      counter
	pluginClient *plugin.Client
	runner       runner.Runner
}

func NewService() pb.WorkerServer {
	var c counter
	return &Service{
		counter: c,
	}
}

func (s *Service) Distribute(ctx context.Context, req *pb.DistributeRequest) (*empty.Empty, error) {
	if err := s.generatePlugin(req.Plugin); err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *Service) generatePlugin(b []byte) error {
	fileErr := func(err error) error {
		return status.Errorf(codes.InvalidArgument, "cannot create plugin file: %+v", err)
	}
	if err := os.MkdirAll(pluginsDir, 0777); err != nil {
		return fileErr(err)
	}

	if s.pluginClient != nil {
		s.pluginClient.Kill()
	}
	pluginPath := filepath.Join(pluginsDir, "plugin")
	f, err := os.Create(pluginPath)
	if err != nil {
		return fileErr(err)
	}
	if _, err := f.Write(b); err != nil {
		return fileErr(err)
	}
	if err := f.Close(); err != nil {
		return fileErr(err)
	}
	if err := os.Chmod(pluginPath, 0755); err != nil {
		return fileErr(err)
	}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: runner.HandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			"runner": &runner.Plugin{},
		},
		Cmd: exec.Command(pluginPath),
	})
	s.pluginClient = client

	rpcClient, err := client.Client()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "cannot get client: %+v", err)
	}
	raw, err := rpcClient.Dispense("runner")
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "cannot dispense runner: %+v", err)
	}
	r, ok := raw.(runner.Runner)
	if !ok {
		return status.Error(codes.InvalidArgument, "cannot assertion to plugin")
	}
	s.runner = r

	return nil
}

func (s *Service) Execute(ctx context.Context, req *pb.ExecuteRequest) (*empty.Empty, error) {
	if s.runner == nil {
		return nil, status.Error(codes.InvalidArgument, "please distribute first")
	}

	s.resultCh = make(chan []*pb.Result, 1000)
	s.cancelCh = make(chan struct{}, 1)
	s.counter.Reset()

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
	log.Print("end threadForTimeout")
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
	log.Print("end threadForCount")
}

func (s *Service) goroutineMethod(req *pb.ExecuteRequest, ch chan struct{}) {
	defer func() {
		<-ch
	}()
	c := s.counter.Increment()
	results, err := s.runner.Run(c, req.TimeoutSeconds, req.ExMap)
	if err != nil {
		log.Printf("runFunc error: %+v", err)
		return
	}
	s.resultCh <- results
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
		case res, ok := <-s.resultCh:
			if !ok {
				break ForLoop
			}
			results = append(results, res...)
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
	return &empty.Empty{}, nil
}
