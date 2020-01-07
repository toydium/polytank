package controller

import (
	"context"
	"io"
	"log"
	"sync"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	mtx       *sync.RWMutex
	workers   map[string]*worker
	addresses map[string]string
	execute   *pb.ExecuteRequest
}

type worker struct {
	info   *pb.WorkerInfo
	client pb.WorkerClient
}

func NewService() *Service {
	return &Service{
		mtx:       &sync.RWMutex{},
		workers:   map[string]*worker{},
		addresses: map[string]string{},
	}
}

func (s *Service) connectWorker(addr string) (*worker, error) {
	id, exists := s.addresses[addr]
	if exists {
		return s.workers[id], nil
	}

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	client := pb.NewWorkerClient(conn)

	id = uuid.New().String()
	worker := &worker{
		info: &pb.WorkerInfo{
			Uuid:    id,
			Address: addr,
			Status:  pb.WorkerInfo_INITIALIZED,
		},
		client: client,
	}
	s.addresses[addr] = id
	s.workers[id] = worker

	return worker, nil
}

func (s *Service) Status(ctx context.Context, _ *empty.Empty) (*pb.StatusResponse, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	var workers []*pb.WorkerInfo
	for _, worker := range s.workers {
		workers = append(workers, worker.info)
	}

	return &pb.StatusResponse{
		Workers: workers,
	}, nil
}

func (s *Service) AddWorker(ctx context.Context, req *pb.AddWorkerRequest) (*pb.AddWorkerResponse, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	var workers []*pb.WorkerInfo
	for _, addr := range req.Addresses {
		worker, err := s.connectWorker(addr)
		if err != nil {
			return nil, status.Errorf(codes.Aborted, "cannot connect client: %s", addr)
		}
		workers = append(workers, worker.info)
	}

	return &pb.AddWorkerResponse{
		Workers: workers,
	}, nil
}

func (s *Service) SetExecuteRequest(ctx context.Context, req *pb.ExecuteRequest) (*empty.Empty, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.execute = req
	return &empty.Empty{}, nil
}

func (s *Service) SetPlugin(ctx context.Context, req *pb.DistributeRequest) (*pb.DistributeAllResponse, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	var workers []*pb.WorkerInfo
	for _, worker := range s.workers {
		client := worker.client
		if _, err := client.Distribute(ctx, req); err != nil {
			return nil, err
		}
		worker.info.Status = pb.WorkerInfo_DISTRIBUTED
		workers = append(workers, worker.info)
	}

	return &pb.DistributeAllResponse{
		Workers: workers,
	}, nil
}

func (s *Service) Start(ctx context.Context, req *pb.StartRequest) (*pb.StartResponse, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	var workers []*pb.WorkerInfo
	for _, id := range req.Uuids {
		worker := s.workers[id]
		if worker == nil {
			continue
		}
		if worker.info.Status == pb.WorkerInfo_RUNNING {
			continue
		}

		client := worker.client
		if _, err := client.Execute(ctx, s.execute); err != nil {
			return nil, err
		}
		worker.info.Status = pb.WorkerInfo_RUNNING
		workers = append(workers, worker.info)
	}

	return &pb.StartResponse{
		Workers: workers,
	}, nil
}

func (s *Service) Wait(req *pb.ControllerWaitRequest, stream pb.Controller_WaitServer) error {
	s.mtx.RLock()
	worker := s.workers[req.Uuid]
	s.mtx.RUnlock()

	if worker == nil {
		return status.Errorf(codes.NotFound, "cannot find worker: %s", req.Uuid)
	}
	if worker.info.Status != pb.WorkerInfo_RUNNING {
		return status.Errorf(codes.InvalidArgument, "not running: %s", req.Uuid)
	}

	client := worker.client
	ctx := context.TODO()
	cStream, err := client.Wait(ctx, &empty.Empty{})
	if err != nil {
		return err
	}

	for {
		res, err := cStream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		if err := stream.Send(&pb.ControllerWaitResponse{
			Uuid:         req.Uuid,
			WaitResponse: res,
		}); err != nil {
			return err
		}
		log.Printf("current: %d", res.Current)

		if !res.IsContinue {
			break
		}
	}

	worker.info.Status = pb.WorkerInfo_SUCCESS
	return nil
}

func (s *Service) Stop(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	for _, worker := range s.workers {
		if _, err := worker.client.Stop(ctx, &empty.Empty{}); err != nil {
			return nil, err
		}
		worker.info.Status = pb.WorkerInfo_FAILURE
	}

	return &empty.Empty{}, nil
}
