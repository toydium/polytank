package controller

import (
	"context"
	"io"
	"log"
	"sync"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"github.com/mattn/go-pubsub"
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
	ps        *pubsub.PubSub
}

type worker struct {
	mtx    *sync.RWMutex
	info   *pb.WorkerInfo
	conn   *grpc.ClientConn
	client pb.WorkerClient
}

func (w *worker) setInfo(info *pb.WorkerInfo) {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.info = info
}
func (w *worker) setClient(client pb.WorkerClient) {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.client = client
}
func (w *worker) getStatus() pb.WorkerInfo_Status {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	return w.info.Status
}
func (w *worker) setStatus(status pb.WorkerInfo_Status) {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	oldStatus := w.info.Status
	if status == pb.WorkerInfo_SUCCESS || status == pb.WorkerInfo_FAILURE {
		if oldStatus != pb.WorkerInfo_RUNNING {
			return
		}
	}
	w.info.Status = status
}

func NewService() *Service {
	return &Service{
		mtx:       &sync.RWMutex{},
		workers:   map[string]*worker{},
		addresses: map[string]string{},
		ps:        pubsub.New(),
	}
}

func (s *Service) connectWorker(addr string) (*worker, error) {
	id, exists := s.addresses[addr]
	if exists {
		worker := s.workers[id]
		worker.conn.Close()
	}

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	client := pb.NewWorkerClient(conn)

	if id == "" {
		id = uuid.New().String()
	}
	worker := &worker{
		mtx:  &sync.RWMutex{},
		conn: conn,
	}
	worker.setInfo(&pb.WorkerInfo{
		Uuid:    id,
		Address: addr,
		Status:  pb.WorkerInfo_INITIALIZED,
	})
	worker.setClient(client)
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
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	var workers []*pb.WorkerInfo
	for _, worker := range s.workers {
		client := worker.client
		if _, err := client.Distribute(ctx, req); err != nil {
			return nil, err
		}
		worker.setStatus(pb.WorkerInfo_DISTRIBUTED)
		workers = append(workers, worker.info)
	}

	return &pb.DistributeAllResponse{
		Workers: workers,
	}, nil
}

func (s *Service) Start(ctx context.Context, _ *empty.Empty) (*pb.StartResponse, error) {
	var workers []*pb.WorkerInfo
	for i, w := range s.workers {
		id := i
		worker := w

		workers = append(workers, worker.info)

		st := worker.getStatus()
		if st == pb.WorkerInfo_INITIALIZED || st == pb.WorkerInfo_RUNNING {
			continue
		}
		worker.setStatus(pb.WorkerInfo_RUNNING)

		client := worker.client
		if _, err := client.Execute(ctx, s.execute); err != nil {
			return nil, err
		}

		go func() {
			defer s.ps.Pub(worker.info)

			ctx := context.Background()
			res, err := client.Wait(ctx, &empty.Empty{})
			if err != nil {
				log.Printf("wait[%s] err: %+v", id, err)
				worker.setStatus(pb.WorkerInfo_FAILURE)
				return
			}
			defer res.CloseSend()

			for {
				wr, err := res.Recv()
				if err != nil {
					if err == io.EOF {
						break
					}
					log.Printf("wait recv[%s] err: %+v", id, err)
					worker.setStatus(pb.WorkerInfo_FAILURE)
					break
				}
				s.ps.Pub(&pb.ControllerWaitResponse{
					Uuid:         id,
					WaitResponse: wr,
				})
				if !wr.IsContinue {
					break
				}
			}
			worker.setStatus(pb.WorkerInfo_SUCCESS)
			log.Printf("finish execution[%s]", id)
		}()
	}

	return &pb.StartResponse{
		Workers: workers,
	}, nil
}

func (s *Service) isFinishedAllWorkers() bool {
	isFinish := true
	for _, w := range s.workers {
		st := w.getStatus()
		if st != pb.WorkerInfo_SUCCESS && st != pb.WorkerInfo_FAILURE {
			isFinish = false
		}
	}
	return isFinish
}

func (s *Service) Wait(req *pb.ControllerWaitRequest, stream pb.Controller_WaitServer) error {
	if s.isFinishedAllWorkers() {
		return status.Error(codes.Unavailable, "not found running workers")
	}

	sendFunc := func(res *pb.ControllerWaitResponse) {
		if err := stream.Send(res); err != nil {
			log.Printf("send err: %+v", err)
		}
	}
	finishCh := make(chan bool, 1)
	checkFinishFunc := func(res *pb.WorkerInfo) {
		if s.isFinishedAllWorkers() {
			finishCh <- true
		}
	}
	defer func() {
		s.ps.Leave(sendFunc)
		s.ps.Leave(checkFinishFunc)
	}()

	if err := s.ps.Sub(sendFunc); err != nil {
		return err
	}
	if err := s.ps.Sub(checkFinishFunc); err != nil {
		return err
	}

	log.Print("wait for finish all workers")
	<-finishCh
	return nil
}

func (s *Service) Stop(ctx context.Context, _ *empty.Empty) (*empty.Empty, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	for _, worker := range s.workers {
		if worker.getStatus() != pb.WorkerInfo_RUNNING {
			continue
		}
		if _, err := worker.client.Stop(ctx, &empty.Empty{}); err != nil {
			return nil, err
		}
		worker.setStatus(pb.WorkerInfo_FAILURE)
	}

	return &empty.Empty{}, nil
}
