package metrics

import (
	"context"
	"encoding/json"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yunify/hostnic-cni/pkg/allocator"
	"github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
	"k8s.io/klog/v2"
)

type HostnicMetricsManager struct {
	kubeclient           kubernetes.Interface
	ipamclient           ipam.IPAMClient
	oddPodCount          *float64
	HostnicVxnetCount    *prometheus.Desc
	HostnicVxnetPodCount *prometheus.Desc
	HostnicSubnetCount   *prometheus.Desc
	HostnicOddPodCount   *prometheus.Desc
}

type HostnicVxnetInfo struct {
	Node  string
	Vxnet string
	Phase string
	Mac   string
}

type HostnicVxnetPodInfo struct {
	Node      string
	Vxnet     string
	Namespace string
	Name      string
	Container string
	Ip        string
}

type HostnicSubnetInfo struct {
	Name        string
	Vxnet       string
	Namespace   string
	Mode        string
	Capacity    float64
	Unallocated float64
	Allocate    float64
	Reserved    float64
}

type HostnicOddPodInfo struct {
	Node  string
	Count float64
}

func (c *HostnicMetricsManager) GenerateMetrics() ([]HostnicVxnetInfo, []HostnicVxnetPodInfo, []HostnicSubnetInfo, HostnicOddPodInfo) {
	nics := allocator.Alloc.GetNics()
	var hostnicVxnetInfos []HostnicVxnetInfo
	var hostnicVxnetPodInfos []HostnicVxnetPodInfo
	var hostnicSubnetInfos []HostnicSubnetInfo
	node := os.Getenv("MY_NODE_NAME")
	for _, nic := range nics {
		hostnicVxnetInfos = append(hostnicVxnetInfos, HostnicVxnetInfo{
			Node:  node,
			Vxnet: nic.Nic.VxNet.ID,
			Phase: nic.Nic.Phase.String(),
			Mac:   nic.Nic.HardwareAddr,
		})
		for _, pod := range nic.Pods {
			hostnicVxnetPodInfos = append(hostnicVxnetPodInfos, HostnicVxnetPodInfo{
				Node:      node,
				Vxnet:     nic.Nic.VxNet.ID,
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Container: pod.Containter,
				Ip:        pod.PodIP,
			})
		}
	}

	//get hostnic-ipam-config configmap
	hostnicOddPodInfo := HostnicOddPodInfo{
		Node:  node,
		Count: *c.oddPodCount,
	}
	cm, err := c.kubeclient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Get(context.TODO(), constants.IPAMConfigName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("get hostnic-ipam-config configmap failed: %v", err)
		return hostnicVxnetInfos, hostnicVxnetPodInfos, hostnicSubnetInfos, hostnicOddPodInfo
	}
	var datas map[string][]string
	if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &datas); err != nil {
		klog.Errorf("unmarshal hostnic-ipam-config data failed: %v", err)
		return hostnicVxnetInfos, hostnicVxnetPodInfos, hostnicSubnetInfos, hostnicOddPodInfo
	}
	for _, ippool := range c.ipamclient.GetAllPools() {
		blocks, err := c.ipamclient.ListBlocks(ippool.Name)
		if err != nil {
			continue
		}
		for _, block := range blocks {
			cap := float64(block.NumAddresses())
			free := float64(block.NumFreeAddresses())
			reserved := float64(block.NumReservedAddresses())
			allocate := cap - free - reserved
			hostnicSubnetInfos = append(hostnicSubnetInfos, HostnicSubnetInfo{
				Name:        block.Name,
				Vxnet:       block.Labels[v1alpha1.IPPoolNameLabel],
				Namespace:   getNamespaceByBlock(block.Name, datas),
				Capacity:    cap,
				Unallocated: free,
				Allocate:    allocate,
				Reserved:    reserved,
			})
		}
	}

	return hostnicVxnetInfos, hostnicVxnetPodInfos, hostnicSubnetInfos, hostnicOddPodInfo
}

func (c *HostnicMetricsManager) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.HostnicVxnetCount
	ch <- c.HostnicVxnetPodCount
	ch <- c.HostnicSubnetCount
	ch <- c.HostnicOddPodCount
}

func (c *HostnicMetricsManager) Collect(ch chan<- prometheus.Metric) {
	hostnicVxnetInfos, hostnicVxnetPodInfos, hostnicSubnetInfos, hostnicOddPodInfo := c.GenerateMetrics()
	for _, hostnicVxnetInfo := range hostnicVxnetInfos {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicVxnetCount,
			prometheus.GaugeValue,
			1,
			hostnicVxnetInfo.Node,
			hostnicVxnetInfo.Vxnet,
			hostnicVxnetInfo.Phase,
			hostnicVxnetInfo.Mac,
		)
	}
	for _, hostnicVxnetPodInfo := range hostnicVxnetPodInfos {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicVxnetPodCount,
			prometheus.GaugeValue,
			1,
			hostnicVxnetPodInfo.Node,
			hostnicVxnetPodInfo.Vxnet,
			hostnicVxnetPodInfo.Namespace,
			hostnicVxnetPodInfo.Name,
			hostnicVxnetPodInfo.Container,
			hostnicVxnetPodInfo.Ip,
		)
	}
	for _, hostnicSubnetInfo := range hostnicSubnetInfos {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicSubnetCount,
			prometheus.GaugeValue,
			hostnicSubnetInfo.Capacity,
			hostnicSubnetInfo.Name,
			hostnicSubnetInfo.Vxnet,
			hostnicSubnetInfo.Namespace,
			constants.MetricsSubnetModeCapacity,
		)
		ch <- prometheus.MustNewConstMetric(
			c.HostnicSubnetCount,
			prometheus.GaugeValue,
			hostnicSubnetInfo.Unallocated,
			hostnicSubnetInfo.Name,
			hostnicSubnetInfo.Vxnet,
			hostnicSubnetInfo.Namespace,
			constants.MetricsSubnetModeUnallocated,
		)
		ch <- prometheus.MustNewConstMetric(
			c.HostnicSubnetCount,
			prometheus.GaugeValue,
			hostnicSubnetInfo.Allocate,
			hostnicSubnetInfo.Name,
			hostnicSubnetInfo.Vxnet,
			hostnicSubnetInfo.Namespace,
			constants.MetricsSubnetModeAllocate,
		)
		ch <- prometheus.MustNewConstMetric(
			c.HostnicSubnetCount,
			prometheus.GaugeValue,
			hostnicSubnetInfo.Reserved,
			hostnicSubnetInfo.Name,
			hostnicSubnetInfo.Vxnet,
			hostnicSubnetInfo.Namespace,
			constants.MetricsSubnetModeReserved,
		)
		ch <- prometheus.MustNewConstMetric(
			c.HostnicSubnetCount,
			prometheus.GaugeValue,
			1,
			hostnicSubnetInfo.Name,
			hostnicSubnetInfo.Vxnet,
			hostnicSubnetInfo.Namespace,
			constants.MetricsSubnetModeCount,
		)
	}
	ch <- prometheus.MustNewConstMetric(
		c.HostnicOddPodCount,
		prometheus.GaugeValue,
		hostnicOddPodInfo.Count,
		hostnicOddPodInfo.Node,
	)
}

func NewHostnicMetricsManager(kubeclient kubernetes.Interface, ipamclient ipam.IPAMClient, oddPodCount *float64) *HostnicMetricsManager {
	return &HostnicMetricsManager{
		kubeclient:  kubeclient,
		ipamclient:  ipamclient,
		oddPodCount: oddPodCount,
		HostnicVxnetCount: prometheus.NewDesc(
			"hostnic_vxnet_count",
			"describe vxnet in node with hostnic cni",
			[]string{"node_name", "vxnet_name", "phase", "mac"},
			prometheus.Labels{},
		),
		HostnicVxnetPodCount: prometheus.NewDesc(
			"hostnic_vxnet_pod_count",
			"describe pod in node with hostnic cni",
			[]string{"node_name", "vxnet_name", "pod_namespace", "pod_name", "pod_containerid", "ip"},
			prometheus.Labels{},
		),
		HostnicSubnetCount: prometheus.NewDesc(
			"hostnic_subnet_count",
			"describe subnet in cluster with hostnic cni",
			[]string{"subnet_name", "vxnet_name", "related_namespace", "mode"},
			prometheus.Labels{},
		),
		HostnicOddPodCount: prometheus.NewDesc(
			"hostnic_odd_pod_count",
			"describe odd pod in node with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
	}
}

func getNamespaceByBlock(block string, datas map[string][]string) string {
	for ns, subnets := range datas {
		for _, subnet := range subnets {
			if block == subnet {
				return ns
			}
		}
	}
	//it means block name is default vxnet,and many ns can use this vxnet,so return an uniform name
	return constants.MetricsSubnetDefaultNamespace
}
