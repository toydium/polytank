package main

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"sort"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc"
)

func main() {
	var (
		addr string
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:33333", "controller address")
	flag.Parse()

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		panic(err)
	}

	c := pb.NewControllerClient(conn)
	ctx := context.TODO()
	command := flag.Arg(0)
	switch command {
	case "status":
		res, err := c.Status(ctx, &empty.Empty{})
		if err != nil {
			panic(err)
		}
		log.Print(res.String())
	case "add-worker":
		var addresses []string
		for i := 1; i <= 100; i++ {
			addr := flag.Arg(i)
			if addr == "" {
				break
			}
			addresses = append(addresses, addr)
		}
		req := &pb.AddWorkerRequest{
			Addresses: addresses,
		}
		res, err := c.AddWorker(ctx, req)
		if err != nil {
			panic(err)
		}
		log.Print(res.String())
	case "set-execute-request":
		req := &pb.ExecuteRequest{}
		if err := jsonpb.UnmarshalString(flag.Arg(1), req); err != nil {
			panic(err)
		}
		if _, err := c.SetExecuteRequest(ctx, req); err != nil {
			panic(err)
		}
		log.Print("set execute request")
	case "set-plugin":
		b, err := ioutil.ReadFile(flag.Arg(1))
		if err != nil {
			panic(err)
		}
		req := &pb.DistributeRequest{
			Plugin: b,
		}
		if _, err := c.SetPlugin(ctx, req); err != nil {
			panic(err)
		}
		log.Print("set plugin")
	case "start":
		var uuids []string
		for i := 1; i <= 100; i++ {
			uuid := flag.Arg(i)
			if uuid == "" {
				break
			}
			uuids = append(uuids, uuid)
		}
		req := &pb.StartRequest{
			Uuids: uuids,
		}
		res, err := c.Start(ctx, req)
		if err != nil {
			panic(err)
		}
		log.Print(res.String())
	case "stop":
		if _, err := c.Stop(ctx, &empty.Empty{}); err != nil {
			panic(err)
		}
		log.Print("stopped")
	case "wait":
		req := &pb.ControllerWaitRequest{
			Uuid: flag.Arg(1),
		}
		res, err := c.Wait(ctx, req)
		if err != nil {
			panic(err)
		}
		totalUSec := uint64(0)
		totalCount := 0
		var unixTimestamps []int64
		for {
			r, err := res.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				panic(err)
			}
			for _, result := range r.WaitResponse.Results {
				totalUSec += uint64(result.ElapsedTimeUsec)
				totalCount++
				unixTimestamps = append(unixTimestamps, result.StartTimestampUsec)
			}
			log.Printf("current: %d", r.WaitResponse.Current)
			log.Printf("results: %d", len(r.WaitResponse.Results))
			log.Printf("is_continue: %v", r.WaitResponse.IsContinue)
			log.Print("=====================")
			if !r.WaitResponse.IsContinue {
				break
			}
		}
		log.Printf("total_count: %d", totalCount)
		sec := float64(totalUSec) / float64(time.Second)
		log.Printf("total_elapsed_sec: %f", sec)
		sort.Slice(unixTimestamps, func(i, j int) bool {
			return unixTimestamps[i] < unixTimestamps[j]
		})
		first := unixTimestamps[0]
		last := unixTimestamps[len(unixTimestamps)-1]
		executedSec := (last - first) / int64(time.Second)
		log.Printf("executed_sec: %d", executedSec)
		log.Printf("rps: %f", float64(totalCount)/float64(executedSec))
	default:
		flag.Usage()
	}
}
