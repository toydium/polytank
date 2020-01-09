package controller

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/mattn/go-pubsub"
	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	workers   *worker.ControlledWorkers
	execute   *pb.ExecuteRequest
	ps        *pubsub.PubSub
	responses *responses
	logger    hclog.Logger
}

type responses struct {
	mtx   *sync.RWMutex
	slice []*pb.ControllerWaitResponse
}

func (r *responses) Reset() {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.slice = []*pb.ControllerWaitResponse{}
}
func (r *responses) Add(c *pb.ControllerWaitResponse) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.slice = append(r.slice, c)
}
func (r *responses) Each(f func(c *pb.ControllerWaitResponse) error) error {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	for _, c := range r.slice {
		if err := f(c); err != nil {
			return err
		}
	}
	return nil
}

func NewService() *Service {
	s := &Service{
		workers: worker.NewControlledWorkers(),
		ps:      pubsub.New(),
		responses: &responses{
			mtx:   &sync.RWMutex{},
			slice: []*pb.ControllerWaitResponse{},
		},
		logger: hclog.New(&hclog.LoggerOptions{
			Name:            "polytank-controller",
			Level:           hclog.Debug,
			Output:          os.Stdout,
			JSONFormat:      true,
			IncludeLocation: true,
		}),
	}

	appendSliceFunc := func(res *pb.ControllerWaitResponse) {
		s.responses.Add(res)
	}
	if err := s.ps.Sub(appendSliceFunc); err != nil {
		s.logger.Error("cannot start subscription for WaitResponse")
		return nil
	}

	return s
}

func (s *Service) connectWorker(addr string) (*worker.ControlledWorker, error) {
	s.workers.Delete(addr)

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	info := &pb.WorkerInfo{
		Uuid:    uuid.New().String(),
		Address: addr,
		Status:  pb.WorkerInfo_INITIALIZED,
	}
	cw := worker.NewControlledWorker(info, conn)
	s.workers.Set(cw)

	return cw, nil
}

func (s *Service) Status(ctx context.Context, _ *empty.Empty) (*pb.StatusResponse, error) {
	var workers []*pb.WorkerInfo
	s.workers.Each(func(w *worker.ControlledWorker) {
		workers = append(workers, w.Info())
	})

	return &pb.StatusResponse{
		Workers: workers,
	}, nil
}

func (s *Service) AddWorker(ctx context.Context, req *pb.AddWorkerRequest) (*pb.AddWorkerResponse, error) {
	var workers []*pb.WorkerInfo
	for _, addr := range req.Addresses {
		cw, err := s.connectWorker(addr)
		if err != nil {
			return nil, status.Errorf(codes.Aborted, "cannot connect client: %s", addr)
		}
		workers = append(workers, cw.Info())
	}

	return &pb.AddWorkerResponse{
		Workers: workers,
	}, nil
}

func (s *Service) SetExecuteRequest(ctx context.Context, req *pb.ExecuteRequest) (*empty.Empty, error) {
	s.execute = req
	return &empty.Empty{}, nil
}

func (s *Service) SetPlugin(ctx context.Context, req *pb.DistributeRequest) (*pb.DistributeAllResponse, error) {
	var workers []*pb.WorkerInfo
	if err := s.workers.EachWithErr(func(w *worker.ControlledWorker) error {
		client := w.Client()
		if _, err := client.Distribute(ctx, req); err != nil {
			return err
		}
		w.SetStatus(pb.WorkerInfo_DISTRIBUTED)
		workers = append(workers, w.Info())
		return nil
	}); err != nil {
		return nil, err
	}

	return &pb.DistributeAllResponse{
		Workers: workers,
	}, nil
}

func (s *Service) Start(ctx context.Context, req *pb.StartRequest) (*pb.StartResponse, error) {
	s.responses.Reset()

	var workers []*pb.WorkerInfo
	for _, id := range req.Uuids {
		w := s.workers.GetByUUID(id)
		if w == nil {
			continue
		}
		workers = append(workers, w.Info())

		st := w.GetStatus()
		if st == pb.WorkerInfo_RUNNING {
			continue
		}
		if st == pb.WorkerInfo_INITIALIZED {
			s.logger.Warn("not distributed worker", map[string]string{"uuid": id})
			continue
		}

		client := w.Client()
		if _, err := client.Execute(ctx, s.execute); err != nil {
			s.logger.Error("cannot execute worker", map[string]string{"uuid": id})
			w.SetStatus(pb.WorkerInfo_FAILURE)
			continue
		}

		w.SetStatus(pb.WorkerInfo_RUNNING)
		id := id
		go func() {
			mapForLogger := map[string]string{"uuid": id}

			res, err := client.Wait(ctx, &empty.Empty{})
			if err != nil {
				mapForLogger["err"] = err.Error()
				s.logger.Error("error on wait", mapForLogger)
				w.SetStatus(pb.WorkerInfo_FAILURE)
				return
			}
			defer res.CloseSend()
			for {
				wr, err := res.Recv()
				if err != nil {
					if err == io.EOF {
						break
					}
					mapForLogger["err"] = err.Error()
					s.logger.Error("error on wait recv", mapForLogger)
					w.SetStatus(pb.WorkerInfo_FAILURE)
					break
				}
				s.ps.Pub(&pb.ControllerWaitResponse{
					Uuid:         id,
					WaitResponse: wr,
				})
				w.SetCurrent(wr.Current)
				if !wr.IsContinue {
					break
				}
			}
			w.SetStatus(pb.WorkerInfo_SUCCESS)
			s.logger.Debug("finish execution", map[string]string{"id": id})
		}()
	}

	return &pb.StartResponse{
		Workers: workers,
	}, nil
}

func (s *Service) Wait(req *pb.ControllerWaitRequest, stream pb.Controller_WaitServer) error {
	w := s.workers.GetByUUID(req.Uuid)
	if w == nil {
		return status.Errorf(codes.InvalidArgument, "cannot find worker: %s", req.Uuid)
	}

	if err := s.responses.Each(func(c *pb.ControllerWaitResponse) error {
		if c.Uuid != req.Uuid {
			return nil
		}
		return stream.Send(c)
	}); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}

	if w.GetStatus() != pb.WorkerInfo_RUNNING {
		return nil
	}

	finishCh := make(chan bool, 1)
	sendFunc := func(c *pb.ControllerWaitResponse) {
		if c.Uuid != req.Uuid {
			return
		}
		if err := stream.Send(c); err != nil {
			if err == io.EOF {
				finishCh <- true
				return
			}
			s.logger.Error("error on wait send", map[string]string{"err": err.Error()})
		}
		if w.GetStatus() != pb.WorkerInfo_RUNNING {
			finishCh <- true
			return
		}
	}

	if err := s.ps.Sub(sendFunc); err != nil {
		return err
	}
	defer s.ps.Leave(sendFunc)

	s.logger.Debug("wait for finish worker")
	<-finishCh
	return nil
}

func (s *Service) Stop(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	if err := s.workers.EachWithErr(func(w *worker.ControlledWorker) error {
		if w.GetStatus() != pb.WorkerInfo_RUNNING {
			return nil
		}
		if _, err := w.Client().Stop(ctx, &empty.Empty{}); err != nil {
			return err
		}
		w.SetStatus(pb.WorkerInfo_ABORTED)
		return nil
	}); err != nil {
		return nil, err
	}

	return &empty.Empty{}, nil
}
