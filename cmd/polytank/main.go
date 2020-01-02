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

	"github.com/toydium/polytank/config"
	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/worker"
	"google.golang.org/grpc"
)

func main() {
	var (
		pluginPath string
		configPath string
		port       string
		mode       string
	)
	flag.StringVar(&pluginPath, "plugin", "", "plugin(.so) path")
	flag.StringVar(&configPath, "config", "", "config(.yml) path")
	flag.StringVar(&port, "port", "33333", "listen port number")
	flag.StringVar(&mode, "mode", "standalone", "exec mode [worker|controller|standalone]")
	flag.Parse()

	log.Printf("exec mode: %s", mode)
	switch mode {
	case "worker":
		execWorker(port)
	case "controller":
	case "standalone":
		execStandalone(port, pluginPath, configPath)
	default:
		flag.Usage()
	}
}

func execWorker(port string) {
	l, s, err := listenWorkerServer(port)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Printf("start worker on port %s", port)
	go func() {
		if err := s.Serve(l); err != nil {
			log.Printf("closed: %+v", err)
		}
	}()

	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)

	<-sig
	s.GracefulStop()
	log.Printf("shutdown gracefully")
}

func listenWorkerServer(port string) (net.Listener, *grpc.Server, error) {
	workerService := worker.NewService()
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(1024 * 1024 * 100))
	pb.RegisterWorkerServer(grpcServer, workerService)

	addr := ":" + port
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	return l, grpcServer, nil
}

func execStandalone(port, pluginPath, configPath string) {
	l, s, err := listenWorkerServer(port)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Printf("start worker on port %s", port)
	go func() {
		if err := s.Serve(l); err != nil {
			log.Printf("closed: %+v", err)
		}
	}()

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

	time.Sleep(time.Second * 1)

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
		Index:          0,
		Concurrency:    conf.Concurrency,
		TimeoutSeconds: conf.Timeout,
	}
	if conf.MaxCount > 0 {
		req.Max = &pb.ExecuteRequest_Count{Count: conf.MaxCount}
	} else if conf.MaxSeconds > 0 {
		req.Max = &pb.ExecuteRequest_Seconds{Seconds: conf.MaxSeconds}
	} else {
		log.Print("please set max_count or max_seconds")
		os.Exit(1)
	}

	if _, err := client.Execute(ctx, req); err != nil {
		panic(err)
	}

	stream, err := client.Wait(ctx, &pb.WaitRequest{})
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
		results = append(results, resp.Results...)
		if !resp.IsContinue {
			break
		}
	}

	for _, r := range results {
		log.Print(r.String())
	}
	log.Printf("end")
}
