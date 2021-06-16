package server

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/types/current"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"github.com/yunify/hostnic-cni/pkg/allocator"
	conf2 "github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/config"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

type IPAMServer struct {
	conf          conf2.ServerConf
	ipamclient    ipam.IPAMClient
	clusterConfig *config.ClusterConfig
}

func NewIPAMServer(conf conf2.ServerConf, clusterConfig *config.ClusterConfig, ipamclient ipam.IPAMClient) *IPAMServer {
	return &IPAMServer{
		conf:          conf,
		ipamclient:    ipamclient,
		clusterConfig: clusterConfig,
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
		info ipam.PoolInfo
		rst  *current.Result
	)

	log.Infoln(s.clusterConfig.GetConfig())

	if blocks := s.clusterConfig.GetBlocksForAPP(in.Args.Namespace); len(blocks) > 0 {
		rst, err = s.ipamclient.AutoAssignFromBlocks(ipam.AutoAssignArgs{
			HandleID: podKey(in.Args),
			Blocks:   blocks,
			Info:     &info,
		})
		log.Infof("handle server add request (%v) from ipam %v: %v, %v", in.Args, info, rst, err)
	} else if pools := s.clusterConfig.GetDefaultIPPools(); len(pools) > 0 {
		rst, err = s.ipamclient.AutoAssignFromPools(ipam.AutoAssignArgs{
			HandleID: podKey(in.Args),
			Pools:    pools,
			Info:     &info,
		})
		log.Infof("handle server add request (%v) from ipam %v: %v, %v", in.Args, info, rst, err)
	} else {
		log.Infof("handle server add request (%v): pool or block not found", in.Args)
		return nil, fmt.Errorf("pool or block not found")
	}

	log.Infof("handle server add request (%v)", in.Args)
	defer func() {
		log.WithError(err).Infof("handle server add reply (%v %s)", in.Nic, rst.IPs[0].Address.IP.String())
	}()

	in.Args.VxNet = info.IPPool
	in.Args.PodIP = rst.IPs[0].Address.IP.String()
	in.IP = rst.IPs[0].Address.IP.String()
	in.Nic, err = allocator.Alloc.AllocHostNic(in.Args)
	return in, err
}

// DelNetwork handle del pod request
func (s *IPAMServer) DelNetwork(context context.Context, in *rpc.IPAMMessage) (*rpc.IPAMMessage, error) {
	var err error

	log.Infoln(s.clusterConfig.GetConfig())

	if blocks := s.clusterConfig.GetBlocksForAPP(in.Args.Namespace); len(blocks) > 0 {
		err = s.ipamclient.ReleaseByHandle(podKey(in.Args))
		log.Infof("handle server delete request (%v) from blocks %v: %v", in.Args, blocks, err)
	} else if pools := s.clusterConfig.GetDefaultIPPools(); len(pools) > 0 {
		err = s.ipamclient.ReleaseByHandle(podKey(in.Args))
		log.Infof("handle server delete request (%v) from pool %v: %v", in.Args, pools, err)
	} else {
		log.Infof("handle server delete request (%v): pool or block not found", in.Args)
	}

	log.Infof("handle server delete request (%v)", in.Args)
	defer func() {
		log.WithError(err).Infof("handle server delete reply (%v)", in.Nic)
	}()

	in.Nic, in.IP, err = allocator.Alloc.FreeHostNic(in.Args, in.Peek)
	return in, nil
}

func podKey(pod *rpc.PodInfo) string {
	return pod.Namespace + "-" + pod.Name + "-" + pod.Containter
}
