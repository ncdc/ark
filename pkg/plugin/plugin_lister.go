package plugin

import (
	plugin "github.com/hashicorp/go-plugin"
	proto "github.com/heptio/ark/pkg/plugin/generated"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// PluginIdenitifer uniquely identifies a plugin by command, kind, and name.
type PluginIdentifier struct {
	Command string
	Kind    PluginKind
	Name    string
}

// PluginLister lists plugins.
type PluginLister interface {
	ListPlugins() ([]PluginIdentifier, error)
}

// pluginLister implements PluginLister.
type pluginLister struct {
	plugins []PluginIdentifier
}

// NewPluginLister returns a new PluginLister for plugins.
func NewPluginLister(plugins ...PluginIdentifier) PluginLister {
	return &pluginLister{plugins: plugins}
}

// ListPlugins returns the pluginLister's plugins.
func (pl *pluginLister) ListPlugins() ([]PluginIdentifier, error) {
	return pl.plugins, nil
}

// PluginListerPlugin is a go-plugin Plugin for a PluginLister.
type PluginListerPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	impl PluginLister
}

// NewPluginListerPlugin creates a new PluginListerPlugin with impl as the server-side implementation.
func NewPluginListerPlugin(impl PluginLister) *PluginListerPlugin {
	return &PluginListerPlugin{impl: impl}
}

//////////////////////////////////////////////////////////////////////////////
// client code
//////////////////////////////////////////////////////////////////////////////

// GRPCClient returns a PluginLister gRPC client.
func (p *PluginListerPlugin) GRPCClient(c *grpc.ClientConn) (interface{}, error) {
	return &PluginListerGRPCClient{grpcClient: proto.NewPluginListerClient(c)}, nil
}

// PluginListerGRPCClient implements PluginLister and uses a gRPC client to make calls to the plugin server.
type PluginListerGRPCClient struct {
	grpcClient proto.PluginListerClient
}

// ListPlugins uses the gRPC client to request the list of plugins from the server. It translates the protobuf response
// to []PluginIdentifier.
func (c *PluginListerGRPCClient) ListPlugins() ([]PluginIdentifier, error) {
	resp, err := c.grpcClient.ListPlugins(context.Background(), &proto.Empty{})
	if err != nil {
		return nil, err
	}

	ret := make([]PluginIdentifier, len(resp.Plugins))
	for i, id := range resp.Plugins {
		if !allPluginKinds.Has(id.Kind) {
			return nil, errors.Errorf("invalid plugin kind: %s", id.Kind)
		}

		ret[i] = PluginIdentifier{
			Command: id.Command,
			Kind:    PluginKind(id.Kind),
			Name:    id.Name,
		}
	}

	return ret, nil
}

//////////////////////////////////////////////////////////////////////////////
// server code
//////////////////////////////////////////////////////////////////////////////

// GRPCServer registers a PluginLister gRPC server.
func (p *PluginListerPlugin) GRPCServer(s *grpc.Server) error {
	proto.RegisterPluginListerServer(s, &PluginListerGRPCServer{impl: p.impl})
	return nil
}

// PluginListerGRPCServer implements the proto-generated PluginLister gRPC service interface. It accepts gRPC calls,
// forwards them to impl, and translates the responses to protobuf.
type PluginListerGRPCServer struct {
	impl PluginLister
}

// ListPlugins returns a list of registered plugins, delegating to s.impl to perform the listing.
func (s *PluginListerGRPCServer) ListPlugins(ctx context.Context, req *proto.Empty) (*proto.ListPluginsResponse, error) {
	list, err := s.impl.ListPlugins()
	if err != nil {
		return nil, err
	}

	plugins := make([]*proto.PluginIdentifier, len(list))
	for i, id := range list {
		if !allPluginKinds.Has(id.Kind.String()) {
			return nil, errors.Errorf("invalid plugin kind: %s", id.Kind)
		}

		plugins[i] = &proto.PluginIdentifier{
			Command: id.Command,
			Kind:    id.Kind.String(),
			Name:    id.Name,
		}
	}
	ret := &proto.ListPluginsResponse{
		Plugins: plugins,
	}
	return ret, nil
}
