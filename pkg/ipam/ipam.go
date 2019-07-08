package ipam

import (
	"fmt"
	"net"
	"time"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/pkg/errors"
	"github.com/yunify/hostnic-cni/pkg/ipam/datastore"
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/retry"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/types"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

const (
	ipamdgRPCaddress = "127.0.0.1:41080"
	metricsAddress   = "127.0.0.1:41081"
	gracefulTimeout  = 120 * time.Second

	defaultPoolSize    = 3
	defaultMaxPoolSize = 10
)

type nodeInfo struct {
	InstanceID string
	NodeName   string
	primaryNic *types.HostNic
	vxnet      *types.VxNet
	vpc        *types.VPC
}

// IpamD is the core manager in hostnic which store pod ips and nics
type IpamD struct {
	dataStore *datastore.DataStore

	K8sClient     k8sclient.K8sHelper
	qcClient      qcclient.QingCloudAPI
	networkClient networkutils.NetworkAPIs

	nodeInfo
	poolSize           int
	maxPoolSize        int
	supportVPNTraffic  bool
	prepareCloudClient func() (qcclient.QingCloudAPI, error)
}

// NewIpamD create a new IpamD object with default settings
func NewIpamD(clientset kubernetes.Interface) *IpamD {
	return &IpamD{
		dataStore:          datastore.NewDataStore(),
		networkClient:      networkutils.New(),
		poolSize:           defaultPoolSize,
		maxPoolSize:        defaultMaxPoolSize,
		K8sClient:          k8sclient.NewK8sHelper(clientset),
		prepareCloudClient: prepareQingCloudClient,
	}
}

func (s *IpamD) vpcSubnets() []*string {
	vpcSubnets := make([]*string, 0)
	for _, vxnet := range s.vpc.VxNets {
		str := vxnet.Network.String()
		vpcSubnets = append(vpcSubnets, &str)
	}
	return vpcSubnets
}

func prepareQingCloudClient() (qcclient.QingCloudAPI, error) {
	client, err := qcclient.NewQingCloudClient()
	if err != nil {
		return nil, fmt.Errorf("Failed to initiate qingcloud api, err: %v", err)
	}
	return client, nil
}

func (s *IpamD) setup() error {
	var err error
	s.qcClient, err = s.prepareCloudClient()
	if err != nil {
		return err
	}
	s.InstanceID = s.qcClient.GetInstanceID()
	klog.V(2).Infoln("Get current network  info of this node")
	s.vpc, err = s.qcClient.GetNodeVPC()
	if err != nil {
		klog.Errorf("Failed to get vpc router of %s", s.InstanceID)
		return err
	}
	err = s.EnsureVxNet()
	if err != nil {
		klog.Errorf("Failed to ensure vxnet of instance %s", s.InstanceID)
		return err
	}
	s.primaryNic, err = s.qcClient.GetPrimaryNIC()
	if err != nil {
		klog.Errorf("Failed to get primary nic")
		return err
	}
	klog.V(2).Infoln("Setup host network")

	primaryIP := net.ParseIP(s.primaryNic.Address)
	//setup host network
	err = s.networkClient.SetupHostNetwork(s.vpc.Network, s.vpcSubnets(), s.primaryNic.HardwareAddr, &primaryIP)
	if err != nil {
		klog.Error("Failed to setup host network", err)
		return errors.Wrap(err, "ipamd init: failed to setup host network")
	}

	attachedNICs, err := s.qcClient.GetAttachedNICs(s.vxnet.ID)
	if err != nil {
		klog.Errorf("Failed to get attached nics")
		return err
	}
	for _, nic := range attachedNICs {
		err = s.setupNic(nic)
		if err != nil {
			klog.Errorf("Failed to set up nic %s", nic.ID)
			return err
		}
		klog.V(2).Infof("Set up nic %s done", nic.ID)
	}
	var pods []*k8sclient.K8SPodInfo
	//process local pods
	retry.Do(5, time.Second*5, func() error {
		pods, err = s.K8sClient.GetCurrentNodePods()
		if err != nil {
			return err
		}
		allPodsHaveAnIP := true
		for _, pod := range pods {
			if pod.IP == "" {
				klog.Warningf("Pod %s, Namespace %s, has no IP, will retry", pod.Name, pod.Namespace)
				allPodsHaveAnIP = false
			}
		}
		if allPodsHaveAnIP {
			return nil
		}
		klog.V(1).Infoln("Not all pods have ips now, retry again")
		return errors.New("Should retry")
	})
	klog.V(1).Infoln("Prepare local pods")
	err = s.prepareLocalPods(pods)
	if err != nil {
		klog.Errorln("Failed to set up exsit pods")
		return err
	}
	klog.V(1).Infoln("IpamD: Everything is ready")
	return nil
}

func (s *IpamD) prepareLocalPods(pods []*k8sclient.K8SPodInfo) error {
	rules, err := s.networkClient.GetRuleList()
	if err != nil {
		klog.Errorf("During ipamd init: failed to retrieve IP rule list %v", err)
		return nil
	}

	for _, ip := range pods {
		if ip.IP == "" {
			klog.Warningf("Skipping Pod %s, Namespace %s, due to no IP", ip.Name, ip.Namespace)
			continue
		}
		klog.V(1).Infof("Recovered AddNetwork for Pod %s, Namespace %s, Container %s", ip.Name, ip.Namespace, ip.Container)
		_, _, err = s.dataStore.AssignPodIPv4Address(ip)
		if err != nil {
			klog.Warningf("During ipamd init, failed to use pod IP %s returned from Kubelet %v", ip.IP, err)
		}

		// Update ip rules in case there is a change in VPC CIDRs, AWS_VPC_K8S_CNI_EXTERNALSNAT setting
		srcIPNet := net.IPNet{IP: net.ParseIP(ip.IP), Mask: net.IPv4Mask(255, 255, 255, 255)}

		var pbVPCcidrs []string
		for _, cidr := range s.vpcSubnets() {
			pbVPCcidrs = append(pbVPCcidrs, *cidr)
		}
		//append vpn net
		pbVPCcidrs = append(pbVPCcidrs, networkutils.GetVPNNet(ip.IP))
		table := s.getNicIndexByIP(ip.IP)
		if table == -1 {
			klog.Errorf("Cannot get device number of %+v", ip)
			continue
		}
		err = s.networkClient.UpdateRuleListBySrc(rules, srcIPNet, pbVPCcidrs, !s.networkClient.UseExternalSNAT(), table)
		if err != nil {
			klog.Errorf("UpdateRuleListBySrc in nodeInit() failed for IP %s: %v", ip.IP, err)
		}
	}
	return nil
}
func (s *IpamD) setupNic(nic *types.HostNic) error {
	err := s.dataStore.AddNIC(nic.ID, nic.DeviceNumber, nic.IsPrimary)
	if err != nil && err.Error() != datastore.DuplicatedNICError {
		return errors.Wrapf(err, "failed to add NIC %s to data store", nic.ID)
	}
	if !nic.IsPrimary {
		err := s.networkClient.SetupNICNetwork(nic.Address, nic.HardwareAddr, nic.DeviceNumber, s.vxnet.Network.String())
		if err != nil {
			klog.Errorf("Failed to set up nic %s", nic.ID)
			return err
		}
		err = s.dataStore.AddIPv4AddressFromStore(nic.ID, nic.Address)
		if err != nil && err.Error() != datastore.DuplicateIPError {
			klog.Warningf("Failed to increase IP pool, failed to add IP %s to data store", nic.Address)
		}
		return nil
	}
	return nil
}

// StartIPAMD will start all long-running components in IpamD
func (s *IpamD) StartIPAMD(stopCh <-chan struct{}) error {
	err := s.K8sClient.Start(stopCh)
	if err != nil {
		klog.Errorln("Failed to start k8s controller")
		return err
	}
	klog.V(2).Infoln("Begin to set up IPAM")
	return s.setup()
}

// StartGrpcServer starting the GRPC server
func (s *IpamD) StartGrpcServer() error {
	listener, err := net.Listen("tcp", ipamdgRPCaddress)
	if err != nil {
		klog.Errorln("Failed to listen to assigned port")
		return err
	}
	//start up server rpc routine

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(grpc_prometheus.UnaryServerInterceptor),
	)
	handlers := NewGRPCServerHandler(s)
	rpc.RegisterCNIBackendServer(grpcServer, handlers)
	grpc_prometheus.Register(grpcServer)
	go grpcServer.Serve(listener)
	return nil
}

func (s *IpamD) getNicIndexByIP(ip string) int {
	nics := s.dataStore.GetNICInfos().NICIPPools
	for _, nic := range nics {
		for i := range nic.IPv4Addresses {
			if i == ip {
				return nic.DeviceNumber
			}
		}
	}
	return -1
}
