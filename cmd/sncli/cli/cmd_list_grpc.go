package cli

import (
	"context"
	"fmt"
	"log"

	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
)

func (c *CLI) ListGRPCMethods(service string) error {
	clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       c.kr,
			LocalIdentity: c.CfgOpts.Keyring.LocalAddress,
			PeerType:      securekeyx.Simplenode,
			Validator:     c.lumeraClient,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create secure credentials: %w", err)
	}

	grpcClient := grpcclient.NewClient(clientCreds)
	target := credentials.FormatAddressWithIdentity(c.CfgOpts.Supernode.Address, c.CfgOpts.Supernode.GRPCEndpoint)

	conn, err := grpcClient.Connect(context.Background(), target, grpcclient.DefaultClientOptions())
	if err != nil {
		return fmt.Errorf("secure grpc connect failed: %w", err)
	}
	defer conn.Close()

	rc := reflectpb.NewServerReflectionClient(conn)
	stream, err := rc.ServerReflectionInfo(context.Background())
	if err != nil {
		return fmt.Errorf("failed to start reflection stream: %w", err)
	}

	// If no specific service is requested, list all services
	if len(service) == 0 || service == "" {
		if err := stream.Send(&reflectpb.ServerReflectionRequest{
			MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{
				ListServices: "*",
			},
		}); err != nil {
			return fmt.Errorf("failed to send list services request: %w", err)
		}

		resp, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("failed to receive reflection response: %w", err)
		}

		svcResp := resp.GetListServicesResponse()
		fmt.Println("\U0001f4e1 Available gRPC Services on Supernode:")
		for _, svc := range svcResp.Service {
			fmt.Println(" -", svc.Name)
		}
		return nil
	}

	// Describe methods in the specified service
	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: service,
		},
	}); err != nil {
		return fmt.Errorf("failed to send list services request containing symbol %q: %w", service, err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive reflection response: %w", err)
	}

	fileResp := resp.GetFileDescriptorResponse()
	for _, fdBytes := range fileResp.FileDescriptorProto {
		fd := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(fdBytes, fd); err != nil {
			log.Printf("Failed to unmarshal descriptor: %v", err)
			continue
		}
		file, err := protodesc.NewFile(fd, nil)
		if err != nil {
			log.Printf("Failed to interpret descriptor: %v", err)
			continue
		}
		for i := 0; i < file.Services().Len(); i++ {
			svc := file.Services().Get(i)
			if string(svc.FullName()) == service {
				fmt.Printf("\n\U0001F50D Methods in %s:\n", svc.FullName())
				for j := 0; j < svc.Methods().Len(); j++ {
					m := svc.Methods().Get(j)
					fmt.Printf(" - %s(%s) returns (%s)\n",
						m.Name(),
						m.Input().FullName(),
						m.Output().FullName(),
					)
				}
			}
		}
	}

	return nil
}
