package main

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/toydium/polytank/config"
	"github.com/toydium/polytank/controller"
	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/worker"
	"google.golang.org/grpc"
)

const maxMsgSize = 1024 * 1024 * 100

func main() {
	var (
		pluginPath string
		configPath string
		port       string
		mode       string
		waitMSec   uint64
	)
	flag.StringVar(&pluginPath, "plugin", "", "plugin(.so) path")
	flag.StringVar(&configPath, "config", "", "config(.yml) path")
	flag.StringVar(&port, "port", "33333", "listen port number")
	flag.StringVar(&mode, "mode", "standalone", "exec mode [worker|controller|standalone]")
	flag.Uint64Var(&waitMSec, "wait", 1000, "send result wait ticker(msec)")
	flag.Parse()

	log.Printf("exec mode: %s", mode)
	switch mode {
	case "worker":
		execWorker(port, waitMSec)
	case "controller":
		execController(port)
	case "standalone":
		execStandalone(port, pluginPath, configPath, waitMSec)
	default:
		flag.Usage()
	}
}

func serveAndWaitForSignal(l net.Listener, s *grpc.Server) {
	go func() {
		if err := s.Serve(l); err != nil {
			log.Printf("closed: %+v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)

	<-sig
	s.GracefulStop()
	log.Printf("shutdown gracefully")
}

func execWorker(port string, waitMsec uint64) {
	ws := worker.NewService(time.Duration(waitMsec))
	defer ws.DisconnectPlugin()

	l, s, err := listenWorkerServer(port, ws)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Printf("start worker on port %s", port)
	serveAndWaitForSignal(l, s)
}

func listenWorkerServer(port string, ws *worker.Service) (net.Listener, *grpc.Server, error) {
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(1024 * 1024 * 100))
	pb.RegisterWorkerServer(grpcServer, ws)

	addr := ":" + port
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	return l, grpcServer, nil
}

func execController(port string) {
	controllerService := controller.NewService()
	grpcServer := grpc.NewServer(grpc.MaxSendMsgSize(maxMsgSize), grpc.MaxRecvMsgSize(maxMsgSize))
	pb.RegisterControllerServer(grpcServer, controllerService)

	addr := ":" + port
	l, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Printf("start controller on port %s", port)
	serveAndWaitForSignal(l, grpcServer)
}

func execStandalone(port, pluginPath, configPath string, waitMSec uint64) {
	ws := worker.NewService(time.Duration(waitMSec))
	defer ws.DisconnectPlugin()

	l, s, err := listenWorkerServer(port, ws)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Printf("start worker on port %s", port)
	ch := make(chan struct{}, 1)
	go func() {
		ch <- struct{}{}
		if err := s.Serve(l); err != nil {
			log.Printf("closed: %+v", err)
		}
	}()

	<-ch
	var conn *grpc.ClientConn
	for i := 0; i < 5; i++ {
		conn, err = grpc.Dial("127.0.0.1:"+port, grpc.WithInsecure())
		if err == nil {
			break
		}
	}
	if conn == nil {
		log.Printf("cannot connect worker grpc server")
		os.Exit(1)
	}
	defer conn.Close()

	conf, err := config.Load(configPath)
	if err != nil {
		panic(err)
	}

	b, err := ioutil.ReadFile(pluginPath)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	client := pb.NewWorkerClient(conn)
	if _, err := client.Distribute(ctx, &pb.DistributeRequest{
		Plugin: b,
	}); err != nil {
		panic(err)
	}

	req := &pb.ExecuteRequest{
		Concurrency:    conf.Concurrency,
		TimeoutSeconds: conf.Timeout,
		ExMap:          conf.ExMap,
	}
	if conf.MaxCount > 0 {
		req.Max = &pb.ExecuteRequest_Count{Count: conf.MaxCount}
	} else if conf.MaxSeconds > 0 {
		req.Max = &pb.ExecuteRequest_Seconds{Seconds: conf.MaxSeconds}
	} else {
		log.Print("please set max_count or max_seconds")
		os.Exit(1)
	}

	start := time.Now()
	if _, err := client.Execute(ctx, req); err != nil {
		panic(err)
	}

	stream, err := client.Wait(ctx, &empty.Empty{})
	if err != nil {
		panic(err)
	}
	var results []*pb.Result
	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}
		log.Printf("current: %d", resp.Current)
		results = append(results, resp.Results...)
		if !resp.IsContinue {
			break
		}
	}
	end := time.Now()

	totalElapsed := uint64(0)
	failureCount := 0
	for _, r := range results {
		totalElapsed += uint64(r.ElapsedTimeUsec)
		if !r.IsSuccess {
			failureCount++
		}
	}

	length := len(results)
	elapsedTime := float64(end.UnixNano() - start.UnixNano())
	elapsedSec := elapsedTime / float64(time.Second)
	log.Printf("total_count: %d", length)
	log.Printf("elapsed_sec: %f", elapsedSec)
	log.Printf("failure_count: %d", failureCount)
	log.Printf("rps: %f", float64(length)/elapsedSec)
}
