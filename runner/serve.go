package runner

import "github.com/hashicorp/go-plugin"

func Serve(impl Runner) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: HandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			"runner": &Plugin{Impl: impl},
		},
	})
}
