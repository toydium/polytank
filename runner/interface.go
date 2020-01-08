package runner

import (
	"encoding/json"
	"net/rpc"

	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-plugin"
	"github.com/toydium/polytank/pb"
)

type Runner interface {
	Run(index, timeout uint32, exMap map[string]string) ([]*pb.Result, error)
}

type RPCClient struct {
	client *rpc.Client
}

func (c *RPCClient) Run(index, timeout uint32, exMap map[string]string) ([]*pb.Result, error) {
	var resp string

	exMapBytes, err := json.Marshal(exMap)
	if err != nil {
		return nil, err
	}

	args := map[string]interface{}{
		"index":   index,
		"timeout": timeout,
		"exMap":   exMapBytes,
	}
	if err := c.client.Call("Plugin.Run", args, &resp); err != nil {
		return nil, err
	}

	b := []byte(resp)
	results := &pb.Results{}
	if err := proto.Unmarshal(b, results); err != nil {
		return nil, err
	}

	return results.List, nil
}

type RPCServer struct {
	Impl Runner
}

func (s *RPCServer) Run(args map[string]interface{}, resp *string) error {
	index := args["index"].(uint32)
	timeout := args["timeout"].(uint32)
	exMapBytes := args["exMap"].([]byte)
	exMap := map[string]string{}
	if err := json.Unmarshal(exMapBytes, &exMap); err != nil {
		return err
	}

	list, err := s.Impl.Run(index, timeout, exMap)
	if err != nil {
		return err
	}
	results := &pb.Results{List: list}
	b, err := proto.Marshal(results)
	if err != nil {
		return err
	}
	*resp = string(b)
	return nil
}

type Plugin struct {
	Impl Runner
}

func (p *Plugin) Server(broker *plugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

func (p *Plugin) Client(broker *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &RPCClient{client: c}, nil
}

func HandshakeConfig() plugin.HandshakeConfig {
	return plugin.HandshakeConfig{
		ProtocolVersion:  1,
		MagicCookieKey:   "POLYTANK",
		MagicCookieValue: "RUNNER_PLUGIN",
	}
}
