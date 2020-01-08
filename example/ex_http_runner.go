package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/toydium/polytank/pb"
	"github.com/toydium/polytank/runner"
)

type ExHttpRunner struct {
}

func (e *ExHttpRunner) Run(index, timeout uint32, exMap map[string]string) ([]*pb.Result, error) {
	urls := []string{
		"https://www.google.com",
		"https://www.facebook.com",
		"https://www.amazon.com",
	}

	var res []*pb.Result
	for _, url := range urls {
		r := e.accessURL(url)
		r.ElapsedTimeUsec = time.Now().UnixNano() - r.StartTimestampUsec
		res = append(res, r)
	}

	return res, nil
}

func (e *ExHttpRunner) accessURL(url string) *pb.Result {
	res := &pb.Result{
		ProcessName:        fmt.Sprintf("access to %s", url),
		IsSuccess:          false,
		StartTimestampUsec: time.Now().UnixNano(),
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Get(url)
	if err != nil {
		return res
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return res
	}
	log.Printf("response size[%s]: %d", url, len(b))

	res.IsSuccess = true
	return res
}

func main() {
	runner.Serve(&ExHttpRunner{})
}
