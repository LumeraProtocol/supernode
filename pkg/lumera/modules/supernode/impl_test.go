package supernode

import (
	"context"
	"net"
	"testing"

	"github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type testQueryServer struct {
	types.UnimplementedQueryServer
	paramsResp *types.QueryParamsResponse
}

func (t *testQueryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	return t.paramsResp, nil
}

func registerNewQueryServer(server *grpc.Server, srv types.QueryServer) {
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "lumera.supernode.v1.Query",
		HandlerType: (*types.QueryServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Params",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					in := new(types.QueryParamsRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					if interceptor == nil {
						return srv.(types.QueryServer).Params(ctx, in)
					}
					info := &grpc.UnaryServerInfo{
						Server:     srv,
						FullMethod: "/lumera.supernode.v1.Query/Params",
					}
					handler := func(ctx context.Context, req interface{}) (interface{}, error) {
						return srv.(types.QueryServer).Params(ctx, req.(*types.QueryParamsRequest))
					}
					return interceptor(ctx, in, info, handler)
				},
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "lumera/supernode/query.proto",
	}, srv)
}

func setupModule(t *testing.T, register func(*grpc.Server)) *module {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	register(server)

	go func() {
		_ = server.Serve(lis)
	}()

	ctx := context.Background()
	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = lis.Close()
	})

	modIface, err := newModule(conn)
	require.NoError(t, err)

	mod, ok := modIface.(*module)
	require.True(t, ok)
	return mod
}

func TestModuleUsesNewServiceWhenAvailable(t *testing.T) {
	srv := &testQueryServer{paramsResp: &types.QueryParamsResponse{}}
	mod := setupModule(t, func(s *grpc.Server) {
		registerNewQueryServer(s, srv)
	})

	resp, err := mod.GetParams(context.Background())
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, preferenceNew, mod.preference.Load())
}

func TestModuleFallsBackToLegacyService(t *testing.T) {
	srv := &testQueryServer{paramsResp: &types.QueryParamsResponse{}}
	mod := setupModule(t, func(s *grpc.Server) {
		types.RegisterQueryServer(s, srv)
	})

	resp, err := mod.GetParams(context.Background())
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, preferenceLegacy, mod.preference.Load())

	resp, err = mod.GetParams(context.Background())
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, preferenceLegacy, mod.preference.Load())
}
