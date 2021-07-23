package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	log "k8s.io/klog/v2"
	"net/http"

	"github.com/yunify/hostnic-cni/pkg/allocator"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/config"
	"github.com/yunify/hostnic-cni/pkg/metrics"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

type IPAMServer struct {
	conf          conf.ServerConf
	kubeclient    kubernetes.Interface
	ipamclient    ipam.IPAMClient
	clusterConfig *config.ClusterConfig
}

func NewIPAMServer(conf conf.ServerConf, clusterConfig *config.ClusterConfig, kubeclient kubernetes.Interface, ipamclient ipam.IPAMClient) *IPAMServer {
	return &IPAMServer{
		conf:          conf,
		kubeclient:    kubeclient,
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

	//start up metrics server routine
	hostnicMetricsManager := metrics.NewHostnicMetricsManager()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(hostnicMetricsManager)
	gatherers := prometheus.Gatherers{
		reg,
	}
	h := promhttp.HandlerFor(gatherers,
		promhttp.HandlerOpts{
			ErrorLog:      nil,
			ErrorHandling: promhttp.ContinueOnError,
		})
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
	})
	go func() {
		http.ListenAndServe(fmt.Sprintf(":%d", allocator.Alloc.GetMetricsPort()), nil)
	}()

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

	if s.conf.NetworkPolicy == "calico" {
		// patch pod's annotations for calico policy
		if err := s.patchPodIPAnnotations(in.Args.Namespace, in.Args.Name, podIP); err != nil {
			if err := s.ipamclient.ReleaseByHandle(handleID); err != nil {
				log.Errorf("AddNetwork request (%v) ReleaseByHandle failed: %v", in.Args, err)
			}
			return nil, err
		}
	}

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

func (s *IPAMServer) ShowNics(context context.Context, in *rpc.Nothing) (*rpc.NicInfoList, error) {
	log.Info("ShowNics request")
	ret := &rpc.NicInfoList{}
	var err error
	defer func() {
		log.Infof("ShowNics reply:%v %v", ret.Items, err)
	}()
	nics := allocator.Alloc.GetNics()
	for _, v := range nics {
		info := &rpc.NicInfo{
			Id:     v.Nic.ID,
			Vxnet:  v.Nic.VxNet.ID,
			Phase:  v.Nic.Phase.String(),
			Status: v.Nic.Status.String(),
			Pods:   int32(len(v.Pods)),
		}
		ret.Items = append(ret.Items, info)
	}
	return ret, err
}

func (s *IPAMServer) ClearNics(context context.Context, in *rpc.Nothing) (*rpc.Nothing, error) {
	log.Info("ClearNics request")
	err := allocator.Alloc.ClearFreeHostnic(true)
	return in, err
}

func (s *IPAMServer) patchPodIPAnnotations(ns, podName string, ip string) error {
	patch, err := calculateAnnotationPatch(constants.CalicoAnnotationPodIP, ip, constants.CalicoAnnotationPodIPs, ip)
	if err != nil {
		return err
	}

	_, err = s.kubeclient.CoreV1().Pods(ns).Patch(context.TODO(), podName, types.StrategicMergePatchType, patch, metav1.PatchOptions{}, "status")
	if err != nil {
		return err
	}

	return nil
}

func podHandleKey(pod *rpc.PodInfo) string {
	return pod.Namespace + "-" + pod.Name + "-" + pod.Containter
}

func calculateAnnotationPatch(namesAndValues ...string) ([]byte, error) {
	patch := map[string]interface{}{}
	metadata := map[string]interface{}{}
	patch["metadata"] = metadata
	annotations := map[string]interface{}{}
	metadata["annotations"] = annotations

	for i := 0; i < len(namesAndValues); i += 2 {
		annotations[namesAndValues[i]] = namesAndValues[i+1]
	}

	return json.Marshal(patch)
}
