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

	defaultPoolSize    = 5
	defaultNICStep     = 5
	defaultMaxPoolSize = 10
	defaultClusterName = "kubernetes"

	envExtraTags      = "HOSTNIC_EXTRA_TAGS"
	envClusterName    = "HOSTNIC_CLUSTER_NAME"
	envVethPrefix     = "HOSTNIC_VETH_PREFIX"
	defaultVethPrefix = "nic"
	configFileName    = "/host/etc/cni/net.d/10-ahostnic.conflist"
)

type nodeInfo struct {
	InstanceID string
	NodeName   string
	primaryNic *types.HostNic
	vxnet      *types.VxNet
	vpc        *types.VPC
}

type prepareCloudClientType func(*qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error)

// IpamD is the core manager in hostnic which store pod ips and nics
type IpamD struct {
	dataStore *datastore.DataStore

	K8sClient k8sclient.K8sHelper

	networkClient networkutils.NetworkAPIs

	nodeInfo

	//For pool manager
	poolSize    int
	maxPoolSize int
	trigCh      chan udevNotify
	pendingNic  map[string]*types.HostNic
	deletingNic map[string]string

	supportVPNTraffic bool
	extraTags         []string
	clusterName       string
	disableLabel      bool

	vethPrefix string

	prepareCloudClient prepareCloudClientType
	qcClient           qcclient.QingCloudAPI
}

//TODO: High and low water mark should be settable
// NewIpamD create a new IpamD object with default settings
func NewIpamD(clientset kubernetes.Interface) *IpamD {
	return &IpamD{
		dataStore:          datastore.NewDataStore(),
		networkClient:      networkutils.New(),
		pendingNic:         make(map[string]*types.HostNic),
		deletingNic:        make(map[string]string),
		poolSize:           defaultPoolSize,
		maxPoolSize:        defaultMaxPoolSize,
		trigCh:             make(chan udevNotify),
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

func prepareQingCloudClient(config *qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error) {
	client, err := qcclient.NewQingCloudClient(config)
	if err != nil {
		return nil, fmt.Errorf("Failed to initiate qingcloud api, err: %v", err)
	}
	return client, nil
}

func (s *IpamD) setup() error {
	var err error
	var labelConfig *qcclient.LabelResourceConfig

	s.parseEnv()

	//set up qingcloud client
	if s.disableLabel {
		labelConfig = nil
	} else {
		labelConfig = &qcclient.LabelResourceConfig{
			ClusterName: s.clusterName,
			ExtraLabels: s.extraTags,
		}
	}
	s.qcClient, err = s.prepareCloudClient(labelConfig)
	if err != nil {
		return err
	}

	//set up node info
	s.InstanceID = s.qcClient.GetInstanceID()

	s.primaryNic, err = s.qcClient.GetPrimaryNIC()
	if err != nil {
		klog.Errorf("Failed to get primary nic")
		return err
	}
	primaryVxnetId := s.primaryNic.VxNet.ID
	vxnet, err := s.qcClient.GetVxNet(primaryVxnetId)
	if err != nil {
		return err
	}
	s.primaryNic.VxNet = vxnet

	klog.Infoln("Get current network  info of this node")
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

	klog.V(2).Infoln("Setup host network")
	primaryIP := net.ParseIP(s.primaryNic.Address)
	err = s.networkClient.SetupHostNetwork(s.primaryNic.HardwareAddr, &primaryIP)
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

	err = s.prepareLocalPods()
	if err != nil {
		klog.Errorln("Failed to set up exsit pods")
		return err
	}

	klog.V(1).Infoln("IpamD: Everything is ready")
	return nil
}

func (s *IpamD) prepareLocalPods() error {
	var pods []*k8sclient.K8SPodInfo
	var err error
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
	}
	return nil
}

func (s *IpamD) setupNic(nic *types.HostNic) error {
	//check device number
	//TODO： use netlink wrap
	if nic.DeviceNumber <= 0 {
		link, err := types.LinkByMacAddr(nic.HardwareAddr)
		if err != nil {
			s.pendingNic[types.FormatMacAddr(nic.HardwareAddr)] = nic
			return nil
		}
		nic.DeviceNumber = link.Attrs().Index
	}
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

func Start(clientset *kubernetes.Clientset, stopCh chan struct{}) error {
	ipamd := NewIpamD(clientset)

	klog.Infoln("Starting IPAMD")
	err := ipamd.StartIPAMD(stopCh)
	if err != nil {
		return err
	}

	go ipamd.monitor()
	go ipamd.StartReconcileIPPool(stopCh)
	go ipamd.updateVxnet()

	klog.Infoln("Starting Grpc server")
	err = ipamd.StartGrpcServer()
	if err != nil {
		return fmt.Errorf("Failed to start grpc server, err: %s", err.Error())
	}

	klog.Infoln("Writing hostnic configlist")
	//waiting for nics, just wait 20s before starting to check
	time.Sleep(time.Second * 20)
	err = retry.Do(10, time.Second*5, func() error {
		if total, assigned := ipamd.dataStore.GetStats(); total > assigned {
			err = ipamd.WriteCNIConfig()
			if err != nil {
				return fmt.Errorf("Failed to write CNI configlist, err: %s", err.Error())
			}
			return nil
		}
		klog.Infoln("DataStore has no enough ip address, waiting")
		return fmt.Errorf("DataStore has no enough pods")
	})
	if err != nil {
		return err
	}

	//success
	return nil
}
