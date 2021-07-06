package server

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/types/current"
	"google.golang.org/grpc"
	log "k8s.io/klog/v2"

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
		log.Warningf("cannot remove file %s: %v", socketFilePath, err)
	}

	listener, err := net.Listen("unix", socketFilePath)
	if err != nil {
		log.Fatalf("Failed to listen to %s: %v", socketFilePath, err)
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
		err      error
		info     ipam.PoolInfo
		rst      *current.Result
		podIP    string
		handleID string
	)

	log.Infof("AddNetwork request (%v)", in.Args)
	defer func() {
		log.Infof("AddNetwork reply (%s): from (%v) get (%s) nic (%s) %v", handleID, info, podIP, allocator.GetNicKey(in.Nic), err)
	}()

	handleID = podHandleKey(in.Args)
	if blocks := s.clusterConfig.GetBlocksForAPP(in.Args.Namespace); len(blocks) > 0 {
		if rst, err = s.ipamclient.AutoAssignFromBlocks(ipam.AutoAssignArgs{
			HandleID: handleID,
			Blocks:   blocks,
			Info:     &info,
		}); err != nil {
			log.Errorf("AddNetwork request (%v) from blocks failed: %v", in.Args, err)
			return nil, err
		}
	} else if pools := s.clusterConfig.GetDefaultIPPools(); len(pools) > 0 {
		if rst, err = s.ipamclient.AutoAssignFromPools(ipam.AutoAssignArgs{
			HandleID: handleID,
			Pools:    pools,
			Info:     &info,
		}); err != nil {
			log.Errorf("AddNetwork request (%v) from pools failed: %v", in.Args, err)
			return nil, err
		}
	} else {
		log.Errorf("AddNetwork request (%v): pool or block not found", in.Args)
		return nil, fmt.Errorf("pool or block not found")
	}

	podIP = rst.IPs[0].Address.IP.String()
	in.Args.VxNet = info.IPPool
	in.Args.PodIP = podIP
	in.IP = podIP
	in.Nic, err = allocator.Alloc.AllocHostNic(in.Args)
	return in, err
}

// DelNetwork handle del pod request
func (s *IPAMServer) DelNetwork(context context.Context, in *rpc.IPAMMessage) (*rpc.IPAMMessage, error) {
	var (
		err      error
		handleID string
	)

	log.Infof("DelNetwork request (%v)", in.Args)
	defer func() {
		log.Infof("DelNetwork reply (%s): ip (%s) nic (%s) %v", handleID, in.IP, allocator.GetNicKey(in.Nic), err)
	}()

	handleID = podHandleKey(in.Args)
	if err = s.ipamclient.ReleaseByHandle(handleID); err != nil {
		log.Errorf("DelNetwork request (%v) release by %s failed: %v", in.Args, handleID, err)
	}
	in.Nic, in.IP, err = allocator.Alloc.FreeHostNic(in.Args, in.Peek)
	return in, err
}

func podHandleKey(pod *rpc.PodInfo) string {
	return pod.Namespace + "-" + pod.Name + "-" + pod.Containter
}
