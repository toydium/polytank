package main

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"strings"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/toydium/polytank/pb"
	"google.golang.org/grpc"
)

func main() {
	var (
		addr       string
		configPath string
		command    string
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:33333", "controller address")
	flag.StringVar(&configPath, "config", "config.yml", "config yaml path")
	flag.StringVar(&command, "command", "", "exec command")
	flag.Parse()

	arg := flag.Arg(0)
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		panic(err)
	}

	c := pb.NewControllerClient(conn)
	ctx := context.TODO()
	switch command {
	case "status":
		res, err := c.Status(ctx, &empty.Empty{})
		if err != nil {
			panic(err)
		}
		log.Print(res.String())
	case "add-worker":
		req := &pb.AddWorkerRequest{
			Addresses: []string{arg},
		}
		res, err := c.AddWorker(ctx, req)
		if err != nil {
			panic(err)
		}
		log.Print(res.String())
	case "set-plugin":
		b, err := ioutil.ReadFile(arg)
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
		uuids := strings.Fields(arg)
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
			Uuid: arg,
		}
		res, err := c.Wait(ctx, req)
		if err != nil {
			panic(err)
		}
		for {
			r, err := res.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				panic(err)
			}
			log.Print(r.String())
			if !r.WaitResponse.IsContinue {
				break
			}
		}
	default:
		flag.Usage()
	}
}
