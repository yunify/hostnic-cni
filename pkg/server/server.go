package server

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg/allocator"
	conf2 "github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/k8s"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"google.golang.org/grpc"
	"net"
	"os"
)

type IPAMServer struct {
	conf conf2.ServerConf
}

func NewIPAMServer(conf conf2.ServerConf) *IPAMServer {
	return &IPAMServer{
		conf: conf,
	}
}

func (s *IPAMServer) Start(stopCh <-chan struct{}) error {
	go s.run(stopCh)
	return nil
}

// run starting the GRPC server
func (s *IPAMServer) run(stopCh <-chan struct{}) {
	socketFilePath := s.conf.ServerPath

	err := os.Remove(socketFilePath)
	if err != nil {
		log.WithError(err).Warningf("cannot remove file %s", socketFilePath)
	}

	listener, err := net.Listen("unix", socketFilePath)
	if err != nil {
		log.WithError(err).Fatalf("Failed to listen to %s", socketFilePath)
	}

	//start up server rpc routine
	grpcServer := grpc.NewServer()
	rpc.RegisterCNIBackendServer(grpcServer, s)
	go func() {
		grpcServer.Serve(listener)
	}()

	log.Info("server grpc server started")
	<-stopCh
	grpcServer.Stop()
	log.Info("server grpc server stopped")
}

// AddNetwork handle add pod request
func (s *IPAMServer) AddNetwork(context context.Context, in *rpc.IPAMMessage) (*rpc.IPAMMessage, error) {
	var (
		err  error
		info *rpc.PodInfo
	)

	info, err = k8s.K8sHelper.GetPodInfo(in.Args.Namespace, in.Args.Name)
	if err != nil {
		err = fmt.Errorf("cannot get podinfo %s/%s: %v", in.Args.Namespace, in.Args.Name, err)
		return nil, err
	}
	in.Args.NicType = info.NicType
	in.Args.VxNet = info.VxNet
	in.Args.PodIP = info.PodIP

	log.Infof("handle server add request (%v)", in.Args)
	defer func() {
		log.WithError(err).Infof("handle server add reply (%v)", in.Nic)
	}()

	in.Nic, err = allocator.Alloc.AllocHostNic(in.Args)

	return in, err
}

// DelNetwork handle del pod request
func (s *IPAMServer) DelNetwork(context context.Context, in *rpc.IPAMMessage) (*rpc.IPAMMessage, error) {
	var (
		err error
	)

	log.Infof("handle server delete request (%v)", in.Args)
	defer func() {
		log.WithError(err).Infof("handle server delete reply (%v)", in.Nic)
	}()

	in.Nic, err = allocator.Alloc.FreeHostNic(in.Args, in.Peek)

	return in, nil
}
