package worker

import (
	"sync"

	"github.com/cornelk/hashmap"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc"
)

type ControlledWorkers struct {
	mapByID   *hashmap.HashMap
	mapByAddr *hashmap.HashMap
}

func (c *ControlledWorkers) Set(w *ControlledWorker) {
	if c.mapByID.Insert(w.info.Uuid, w) {
		c.mapByAddr.Insert(w.info.Address, w)
	}
}

func (c *ControlledWorkers) GetByUUID(uuid string) *ControlledWorker {
	w, exists := c.mapByID.GetStringKey(uuid)
	if !exists {
		return nil
	}
	return w.(*ControlledWorker)
}

func (c *ControlledWorkers) GetByAddr(addr string) *ControlledWorker {
	w, exists := c.mapByAddr.GetStringKey(addr)
	if !exists {
		return nil
	}
	return w.(*ControlledWorker)
}

func (c *ControlledWorkers) Delete(addr string) {
	w := c.GetByAddr(addr)
	if w == nil {
		return
	}
	w.Close()
	c.mapByAddr.Del(addr)
	c.mapByID.Del(w.info.Uuid)
}

func (c *ControlledWorkers) EachWithErr(f func(w *ControlledWorker) error) error {
	for kv := range c.mapByID.Iter() {
		w := kv.Value.(*ControlledWorker)
		if err := f(w); err != nil {
			return err
		}
	}
	return nil
}

func (c *ControlledWorkers) EachWithBool(f func(w *ControlledWorker) bool) {
	for kv := range c.mapByID.Iter() {
		w := kv.Value.(*ControlledWorker)
		if !f(w) {
			break
		}
	}
}

func (c *ControlledWorkers) Each(f func(w *ControlledWorker)) {
	for kv := range c.mapByID.Iter() {
		w := kv.Value.(*ControlledWorker)
		f(w)
	}
}

func (c *ControlledWorkers) ExistsRunningWorkers() bool {
	exists := false
	c.EachWithBool(func(w *ControlledWorker) bool {
		st := w.GetStatus()
		if st == pb.WorkerInfo_RUNNING {
			exists = true
			return false
		}
		return true
	})
	return exists
}

func NewControlledWorkers() *ControlledWorkers {
	return &ControlledWorkers{
		mapByID:   hashmap.New(1000),
		mapByAddr: hashmap.New(1000),
	}
}

type ControlledWorker struct {
	mtx    *sync.RWMutex
	info   *pb.WorkerInfo
	conn   *grpc.ClientConn
	client pb.WorkerClient
}

func NewControlledWorker(info *pb.WorkerInfo, conn *grpc.ClientConn) *ControlledWorker {
	return &ControlledWorker{
		mtx:    &sync.RWMutex{},
		info:   info,
		conn:   conn,
		client: pb.NewWorkerClient(conn),
	}
}

func (w *ControlledWorker) GetStatus() pb.WorkerInfo_Status {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	return w.info.Status
}
func (w *ControlledWorker) SetStatus(status pb.WorkerInfo_Status) {
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
func (w *ControlledWorker) GetCurrent(i uint32) uint32 {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	return w.info.Current
}
func (w *ControlledWorker) SetCurrent(i uint32) {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.info.Current = i
}
func (w *ControlledWorker) Info() *pb.WorkerInfo {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	return w.info
}
func (w *ControlledWorker) Client() pb.WorkerClient {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	return w.client
}
func (w *ControlledWorker) Close() error {
	w.mtx.RLock()
	defer w.mtx.RUnlock()
	if w.conn == nil {
		return nil
	}
	return w.conn.Close()
}
