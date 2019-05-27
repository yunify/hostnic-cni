package ipam

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"syscall"
	"time"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/server"
	"github.com/yunify/hostnic-cni/pkg/types"
	"google.golang.org/grpc"
	"k8s.io/klog"
)

const (
	ipamdgRPCaddress = "127.0.0.1:41080"
	metricsAddress   = "127.0.0.1:41081"
	gracefulTimeout  = 120 * time.Second
	instanceIDFile   = "/host/etc/qingcloud/instance-id"
)

type nodeInfo struct {
	InstanceID string
	NodeName   string
	vxnet      *types.VxNet
	vpc        *types.VPC
}

type IpamD struct {
	K8sClient k8sclient.K8sHelper
	qcClient  qcclient.QingCloudAPI

	nodeInfo
	poolSize          int
	cleanUpCache      bool
	isInformerStarted bool
}

func NewIpamD() (*IpamD, error) {
	k8s := k8sclient.NewK8sHelper()
	content, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		return nil, fmt.Errorf("Load instance-id from %s error: %v", instanceIDFile, err)
	}
	instanceid := string(content)
	qcclient, err := qcclient.NewQingCloudClient(instanceid)
	if err != nil {
		klog.Errorln("Failed to initiate qingcloud api")
		return nil, err
	}
	klog.V(2).Infoln("Get current vpc info")
	vpc, err := qcclient.GetNodeVPC()
	if err != nil {
		klog.Errorf("Failed to get vpc router of %s", instanceid)
		return nil, err
	}
	return &IpamD{
		K8sClient: k8s,
		qcClient:  qcclient,
		nodeInfo: nodeInfo{
			InstanceID: instanceid,
			vpc:        vpc,
		},
	}, nil
}

func (s *IpamD) StartIPAMD(stopCh <-chan struct{}) error {
	err := s.startK8sClient(stopCh)
	if err != nil {
		klog.Errorln("Failed to start k8s node informer")
		return err
	}
	return s.EnsureVxNet()
}
func (s *IpamD) startK8sClient(stopCh <-chan struct{}) error {
	err := s.K8sClient.Start(stopCh)
	if err != nil {
		return err
	}
	s.isInformerStarted = true
	return nil
}

func (s *IpamD) StartGrpcServer() error {
	listener, err := net.Listen("tcp", ipamdgRPCaddress)
	if err != nil {
		klog.Errorln("Failed to listen to assigned port")
		return err
	}

	//setup nic pool
	nicpool, err := server.NewNicPool(s.poolSize, server.NewQingCloudNicProvider(s.qcClient, s.vxnet.ID),
		server.NewGatewayManager(s.qcClient), server.NicPoolConfig{CleanUpCache: s.cleanUpCache})
	if err != nil {
		klog.Errorln("Failed to create pool.")
		return err
	}
	//start up server rpc routine

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(grpc_prometheus.UnaryServerInterceptor),
	)
	handlers := server.NewDaemonServerHandler(nicpool)
	messages.RegisterNicservicesServer(grpcServer, handlers)
	grpc_prometheus.Register(grpcServer)
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/clearcache", func(writer http.ResponseWriter, request *http.Request) {
		nicpool.CleanUpReadyPool()
		writer.Header().Set("Content-Type", "text/plain")
		writer.Write([]byte("Nic ready pool is cleared .\n"))
	})
	http.HandleFunc("/shutdown", func(writer http.ResponseWriter, request *http.Request) {
		syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		writer.Header().Set("Content-Type", "text/plain")
		writer.Write([]byte("terminate signal is sent .\n"))
	})

	go grpcServer.Serve(listener)
	go http.ListenAndServe(metricsAddress, nil)
	return nil
}
