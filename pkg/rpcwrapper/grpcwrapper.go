package rpcwrapper

import (
	google_grpc "google.golang.org/grpc"
)

type GRPC interface {
	Dial(target string, opts ...google_grpc.DialOption) (*google_grpc.ClientConn, error)
}

type cniGRPC struct{}

func NewGRPC() GRPC {
	return &cniGRPC{}
}

func (*cniGRPC) Dial(target string, opts ...google_grpc.DialOption) (*google_grpc.ClientConn, error) {
	return google_grpc.Dial(target, opts...)
}
