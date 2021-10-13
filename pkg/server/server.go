package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	log "k8s.io/klog/v2"

	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yunify/hostnic-cni/pkg/allocator"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/config"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/metrics"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

type IPAMServer struct {
	conf          conf.ServerConf
	kubeclient    kubernetes.Interface
	ipamclient    ipam.IPAMClient
	clusterConfig *config.ClusterConfig
	metricsPort   int
	oddPodCount   *metrics.OddPodCount
}

func NewIPAMServer(conf conf.ServerConf, clusterConfig *config.ClusterConfig, kubeclient kubernetes.Interface, ipamclient ipam.IPAMClient, metricsPort int) *IPAMServer {
	count := metrics.OddPodCount{
		BlockFailedCount:        0,
		PoolFailedCount:         0,
		NotFoundCount:           0,
		AllocFailedCount:        0,
		FreeFromPoolFailedCount: 0,
		FreeFromHostFailedCount: 0,
	}
	return &IPAMServer{
		conf:          conf,
		kubeclient:    kubeclient,
		ipamclient:    ipamclient,
		clusterConfig: clusterConfig,
		metricsPort:   metricsPort,
		oddPodCount:   &count,
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
	hostnicMetricsManager := metrics.NewHostnicMetricsManager(s.kubeclient, s.ipamclient, s.oddPodCount)
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
		http.ListenAndServe(fmt.Sprintf(":%d", s.metricsPort), nil)
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

func (s *IPAMServer) getK8sPodInfo(podName, podNamespace string) (ipList []string, err error) {
	pod, _ := s.kubeclient.CoreV1().Pods(podNamespace).Get(context.Background(), podName, metav1.GetOptions{})
	ipAddr, ok := pod.Annotations[constants.CalicoAnnotationIpAddr]
	if ipAddr == "" || !ok {
		return ipList, nil
	}
	err = json.Unmarshal([]byte(ipAddr), &ipList)
	if err != nil {
		return nil, fmt.Errorf("failed to parse '%s' as JSON: %s", ipAddr, err)
	}

	for i := 0; i < len(ipList); i++ {
		if net.ParseIP(ipList[i]) == nil {
			return nil, fmt.Errorf("ip[%s] failed to parse err", ipList[i])
		}
	}
	return
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
	ipList, err := s.getK8sPodInfo(in.Args.Name, in.Args.Namespace)
	if err != nil {
		return nil, err
	}

	if blocks := s.clusterConfig.GetBlocksForAPP(in.Args.Namespace); len(blocks) > 0 {
		if len(ipList) > 0 {
			rst, err = s.ipamclient.AssignFixIps(handleID, ipList, nil, blocks, &info)
			if err != nil {
				return nil, err
			}
		} else if rst, err = s.ipamclient.AutoAssignFromBlocks(ipam.AutoAssignArgs{
			HandleID: handleID,
			Blocks:   blocks,
			Info:     &info,
		}); err != nil {
			(*s.oddPodCount).BlockFailedCount = (*s.oddPodCount).BlockFailedCount + 1
			log.Errorf("AddNetwork request (%v) from blocks failed: %v", in.Args, err)
			return nil, err
		}
	} else if pools := s.clusterConfig.GetDefaultIPPools(); len(pools) > 0 {
		if len(ipList) > 0 {
			rst, err = s.ipamclient.AssignFixIps(handleID, ipList, pools, nil, &info)
			if err != nil {
				return nil, err
			}
		} else if rst, err = s.ipamclient.AutoAssignFromPools(ipam.AutoAssignArgs{
			HandleID: handleID,
			Pools:    pools,
			Info:     &info,
		}); err != nil {
			(*s.oddPodCount).PoolFailedCount = (*s.oddPodCount).PoolFailedCount + 1
			log.Errorf("AddNetwork request (%v) from pools failed: %v", in.Args, err)
			return nil, err
		}
	} else {
		(*s.oddPodCount).NotFoundCount = (*s.oddPodCount).NotFoundCount + 1
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
	if err != nil {
		(*s.oddPodCount).AllocFailedCount = (*s.oddPodCount).AllocFailedCount + 1
	}
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
		(*s.oddPodCount).FreeFromPoolFailedCount = (*s.oddPodCount).FreeFromPoolFailedCount + 1
	}
	in.Nic, in.IP, err = allocator.Alloc.FreeHostNic(in.Args, in.Peek)
	if err != nil {
		(*s.oddPodCount).FreeFromHostFailedCount = (*s.oddPodCount).FreeFromHostFailedCount + 1
	}
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
