package rpcwrapper

import (
	"github.com/yunify/hostnic-cni/pkg/rpc"
	grpc "google.golang.org/grpc"
)

type RPC interface {
	NewCNIBackendClient(cc *grpc.ClientConn) rpc.CNIBackendClient
}

type cniRPC struct{}

func New() RPC {
	return &cniRPC{}
}

func (*cniRPC) NewCNIBackendClient(cc *grpc.ClientConn) rpc.CNIBackendClient {
	return rpc.NewCNIBackendClient(cc)
}
