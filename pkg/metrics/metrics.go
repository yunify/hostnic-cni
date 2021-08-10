package metrics

import (
	"context"
	"encoding/json"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/allocator"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

type HostnicMetricsManager struct {
	kubeclient                      kubernetes.Interface
	ipamclient                      ipam.IPAMClient
	oddCount                        *OddPodCount
	HostnicVxnetCount               *prometheus.Desc
	HostnicVxnetPodCount            *prometheus.Desc
	HostnicIpamVxnetAllocator       *prometheus.Desc
	HostnicIpamVxnetUnallocator     *prometheus.Desc
	HostnicIpamVxnetTotal           *prometheus.Desc
	HostnicIpamSubnetAllocator      *prometheus.Desc
	HostnicIpamSubnetUnallocator    *prometheus.Desc
	HostnicIpamSubnetTotal          *prometheus.Desc
	HostnicIpamNamespaceAllocator   *prometheus.Desc
	HostnicIpamNamespaceUnallocator *prometheus.Desc
	HostnicIpamNamespaceTotal       *prometheus.Desc
	HostnicIpamBlockFailed          *prometheus.Desc
	HostnicIpamPoolFailed           *prometheus.Desc
	HostnicIpamNotFound             *prometheus.Desc
	HostnicIpamAllocFailed          *prometheus.Desc
	HostnicIpamFreeFromPoolFailed   *prometheus.Desc
	HostnicIpamFreeFromHostFailed   *prometheus.Desc
}

type OddPodCount struct {
	BlockFailedCount        float64
	PoolFailedCount         float64
	NotFoundCount           float64
	AllocFailedCount        float64
	FreeFromPoolFailedCount float64
	FreeFromHostFailedCount float64
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

type HostnicIpamVxnetAllocator struct {
	Vxnet string
	Node  string
	Count float64
}

type HostnicIpamVxnetUnallocator struct {
	Vxnet string
	Node  string
	Count float64
}

type HostnicIpamVxnetTotal struct {
	Vxnet string
	Node  string
	Count float64
}

type HostnicIpamSubnetAllocator struct {
	Subnet string
	Node   string
	Count  float64
}

type HostnicIpamSubnetUnallocator struct {
	Subnet string
	Node   string
	Count  float64
}

type HostnicIpamSubnetTotal struct {
	Subnet string
	Node   string
	Count  float64
}

type HostnicIpamNamespaceAllocator struct {
	Namespace string
	Node      string
	Count     float64
}

type HostnicIpamNamespaceUnallocator struct {
	Namespace string
	Node      string
	Count     float64
}

type HostnicIpamNamespaceTotal struct {
	Namespace string
	Node      string
	Count     float64
}

type HostnicIpamBlockFailed struct {
	Node  string
	Count float64
}

type HostnicIpamPoolFailed struct {
	Node  string
	Count float64
}

type HostnicIpamNotFound struct {
	Node  string
	Count float64
}

type HostnicIpamAllocFailed struct {
	Node  string
	Count float64
}

type HostnicIpamFreeFromPoolFailed struct {
	Node  string
	Count float64
}

type HostnicIpamFreeFromHostFailed struct {
	Node  string
	Count float64
}

type HostnicMetrics struct {
	HostnicVxnetInfos                []HostnicVxnetInfo
	HostnicVxnetPodInfos             []HostnicVxnetPodInfo
	HostnicIpamVxnetAllocators       []HostnicIpamVxnetAllocator
	HostnicIpamVxnetUnallocators     []HostnicIpamVxnetUnallocator
	HostnicIpamVxnetTotals           []HostnicIpamVxnetTotal
	HostnicIpamSubnetAllocators      []HostnicIpamSubnetAllocator
	HostnicIpamSubnetUnallocators    []HostnicIpamSubnetUnallocator
	HostnicIpamSubnetTotals          []HostnicIpamSubnetTotal
	HostnicIpamNamespaceAllocators   []HostnicIpamNamespaceAllocator
	HostnicIpamNamespaceUnallocators []HostnicIpamNamespaceUnallocator
	HostnicIpamNamespaceTotals       []HostnicIpamNamespaceTotal
	HostnicIpamBlockFailed           HostnicIpamBlockFailed
	HostnicIpamPoolFailed            HostnicIpamPoolFailed
	HostnicIpamNotFound              HostnicIpamNotFound
	HostnicIpamAllocFailed           HostnicIpamAllocFailed
	HostnicIpamFreeFromPoolFailed    HostnicIpamFreeFromPoolFailed
	HostnicIpamFreeFromHostFailed    HostnicIpamFreeFromHostFailed
}

func (c *HostnicMetricsManager) GenerateMetrics() HostnicMetrics {
	nics := allocator.Alloc.GetNics()
	var hostnicVxnetInfos []HostnicVxnetInfo
	var hostnicVxnetPodInfos []HostnicVxnetPodInfo
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
	cm, err := c.kubeclient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Get(context.TODO(), constants.IPAMConfigName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("get configmap %s failed: %v", constants.IPAMConfigName, err)
		return HostnicMetrics{
			HostnicVxnetInfos:    hostnicVxnetInfos,
			HostnicVxnetPodInfos: hostnicVxnetPodInfos,
		}
	}
	var datas map[string][]string
	if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &datas); err != nil {
		klog.Errorf("unmarshal ipam data failed: %v", err)
		return HostnicMetrics{
			HostnicVxnetInfos:    hostnicVxnetInfos,
			HostnicVxnetPodInfos: hostnicVxnetPodInfos,
		}
	}

	var hostnicIpamVxnetAllocators []HostnicIpamVxnetAllocator
	var hostnicIpamVxnetUnallocators []HostnicIpamVxnetUnallocator
	var hostnicIpamVxnetTotals []HostnicIpamVxnetTotal
	var hostnicIpamSubnetAllocators []HostnicIpamSubnetAllocator
	var hostnicIpamSubnetUnallocators []HostnicIpamSubnetUnallocator
	var hostnicIpamSubnetTotals []HostnicIpamSubnetTotal
	var hostnicIpamNamespaceAllocators []HostnicIpamNamespaceAllocator
	var hostnicIpamNamespaceUnallocators []HostnicIpamNamespaceUnallocator
	var hostnicIpamNamespaceTotals []HostnicIpamNamespaceTotal
	allocMap := make(map[string]HostnicIpamNamespaceAllocator)
	unallocMap := make(map[string]HostnicIpamNamespaceUnallocator)
	totalMap := make(map[string]HostnicIpamNamespaceTotal)
	if utils, err := c.ipamclient.GetPoolBlocksUtilization(ipam.GetUtilizationArgs{}); err != nil {
		klog.Errorf("GetPoolBlocksUtilization failed: %v", err)
	} else {
		for _, util := range utils {
			hostnicIpamVxnetAllocators = append(hostnicIpamVxnetAllocators, HostnicIpamVxnetAllocator{
				Vxnet: util.Name,
				Node:  node,
				Count: float64(util.Allocate),
			})
			hostnicIpamVxnetUnallocators = append(hostnicIpamVxnetUnallocators, HostnicIpamVxnetUnallocator{
				Vxnet: util.Name,
				Node:  node,
				Count: float64(util.Unallocated),
			})
			hostnicIpamVxnetTotals = append(hostnicIpamVxnetTotals, HostnicIpamVxnetTotal{
				Vxnet: util.Name,
				Node:  node,
				Count: float64(util.Allocate + util.Unallocated),
			})
			for _, block := range util.Blocks {
				hostnicIpamSubnetAllocators = append(hostnicIpamSubnetAllocators, HostnicIpamSubnetAllocator{
					Subnet: block.Name,
					Node:   node,
					Count:  float64(block.Allocate),
				})
				hostnicIpamSubnetUnallocators = append(hostnicIpamSubnetUnallocators, HostnicIpamSubnetUnallocator{
					Subnet: block.Name,
					Node:   node,
					Count:  float64(block.Unallocated),
				})
				hostnicIpamSubnetTotals = append(hostnicIpamSubnetTotals, HostnicIpamSubnetTotal{
					Subnet: block.Name,
					Node:   node,
					Count:  float64(block.Allocate + block.Unallocated),
				})

				ns := getNamespaceByBlock(block.Name, datas)
				alloc, ok := allocMap[ns]
				if ok {
					alloc.Count += float64(block.Allocate)
					allocMap[ns] = alloc
				} else {
					allocMap[ns] = HostnicIpamNamespaceAllocator{
						Namespace: ns,
						Node:      node,
						Count:     float64(block.Allocate),
					}
				}

				unalloc, ok := unallocMap[ns]
				if ok {
					unalloc.Count += float64(block.Unallocated)
					unallocMap[ns] = unalloc
				} else {
					unallocMap[ns] = HostnicIpamNamespaceUnallocator{
						Namespace: ns,
						Node:      node,
						Count:     float64(block.Unallocated),
					}
				}

				total, ok := totalMap[ns]
				if ok {
					total.Count += float64(block.Allocate + block.Unallocated)
					totalMap[ns] = total
				} else {
					totalMap[ns] = HostnicIpamNamespaceTotal{
						Namespace: ns,
						Node:      node,
						Count:     float64(block.Allocate + block.Unallocated),
					}
				}
			}
		}
	}
	for k, _ := range allocMap {
		hostnicIpamNamespaceAllocators = append(hostnicIpamNamespaceAllocators, allocMap[k])
		hostnicIpamNamespaceUnallocators = append(hostnicIpamNamespaceUnallocators, unallocMap[k])
		hostnicIpamNamespaceTotals = append(hostnicIpamNamespaceTotals, totalMap[k])
	}

	return HostnicMetrics{
		HostnicVxnetInfos:                hostnicVxnetInfos,
		HostnicVxnetPodInfos:             hostnicVxnetPodInfos,
		HostnicIpamVxnetAllocators:       hostnicIpamVxnetAllocators,
		HostnicIpamVxnetUnallocators:     hostnicIpamVxnetUnallocators,
		HostnicIpamVxnetTotals:           hostnicIpamVxnetTotals,
		HostnicIpamSubnetAllocators:      hostnicIpamSubnetAllocators,
		HostnicIpamSubnetUnallocators:    hostnicIpamSubnetUnallocators,
		HostnicIpamSubnetTotals:          hostnicIpamSubnetTotals,
		HostnicIpamNamespaceAllocators:   hostnicIpamNamespaceAllocators,
		HostnicIpamNamespaceUnallocators: hostnicIpamNamespaceUnallocators,
		HostnicIpamNamespaceTotals:       hostnicIpamNamespaceTotals,
		HostnicIpamBlockFailed: HostnicIpamBlockFailed{
			Node:  node,
			Count: c.oddCount.BlockFailedCount,
		},
		HostnicIpamPoolFailed: HostnicIpamPoolFailed{
			Node:  node,
			Count: c.oddCount.PoolFailedCount,
		},
		HostnicIpamNotFound: HostnicIpamNotFound{
			Node:  node,
			Count: c.oddCount.NotFoundCount,
		},
		HostnicIpamAllocFailed: HostnicIpamAllocFailed{
			Node:  node,
			Count: c.oddCount.AllocFailedCount,
		},
		HostnicIpamFreeFromPoolFailed: HostnicIpamFreeFromPoolFailed{
			Node:  node,
			Count: c.oddCount.FreeFromPoolFailedCount,
		},
		HostnicIpamFreeFromHostFailed: HostnicIpamFreeFromHostFailed{
			Node:  node,
			Count: c.oddCount.FreeFromHostFailedCount,
		},
	}
}

func (c *HostnicMetricsManager) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.HostnicVxnetCount
	ch <- c.HostnicVxnetPodCount
	ch <- c.HostnicIpamVxnetAllocator
	ch <- c.HostnicIpamVxnetUnallocator
	ch <- c.HostnicIpamVxnetTotal
	ch <- c.HostnicIpamSubnetAllocator
	ch <- c.HostnicIpamSubnetUnallocator
	ch <- c.HostnicIpamSubnetTotal
	ch <- c.HostnicIpamNamespaceAllocator
	ch <- c.HostnicIpamNamespaceUnallocator
	ch <- c.HostnicIpamNamespaceTotal
	ch <- c.HostnicIpamBlockFailed
	ch <- c.HostnicIpamPoolFailed
	ch <- c.HostnicIpamNotFound
	ch <- c.HostnicIpamAllocFailed
	ch <- c.HostnicIpamFreeFromPoolFailed
	ch <- c.HostnicIpamFreeFromHostFailed
}

func (c *HostnicMetricsManager) Collect(ch chan<- prometheus.Metric) {
	hostnicMetrics := c.GenerateMetrics()
	for _, item := range hostnicMetrics.HostnicVxnetInfos {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicVxnetCount,
			prometheus.GaugeValue,
			1,
			item.Node,
			item.Vxnet,
			item.Phase,
			item.Mac,
		)
	}
	for _, item := range hostnicMetrics.HostnicVxnetPodInfos {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicVxnetPodCount,
			prometheus.GaugeValue,
			1,
			item.Node,
			item.Vxnet,
			item.Namespace,
			item.Name,
			item.Container,
			item.Ip,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamVxnetAllocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamVxnetAllocator,
			prometheus.GaugeValue,
			item.Count,
			item.Vxnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamVxnetUnallocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamVxnetUnallocator,
			prometheus.GaugeValue,
			item.Count,
			item.Vxnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamVxnetTotals {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamVxnetTotal,
			prometheus.GaugeValue,
			item.Count,
			item.Vxnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamSubnetAllocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamSubnetAllocator,
			prometheus.GaugeValue,
			item.Count,
			item.Subnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamSubnetUnallocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamSubnetUnallocator,
			prometheus.GaugeValue,
			item.Count,
			item.Subnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamSubnetTotals {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamSubnetTotal,
			prometheus.GaugeValue,
			item.Count,
			item.Subnet,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamNamespaceAllocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamNamespaceAllocator,
			prometheus.GaugeValue,
			item.Count,
			item.Namespace,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamNamespaceUnallocators {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamNamespaceUnallocator,
			prometheus.GaugeValue,
			item.Count,
			item.Namespace,
			item.Node,
		)
	}
	for _, item := range hostnicMetrics.HostnicIpamNamespaceTotals {
		ch <- prometheus.MustNewConstMetric(
			c.HostnicIpamNamespaceTotal,
			prometheus.GaugeValue,
			item.Count,
			item.Namespace,
			item.Node,
		)
	}
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamBlockFailed,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamBlockFailed.Count,
		hostnicMetrics.HostnicIpamBlockFailed.Node,
	)
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamPoolFailed,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamPoolFailed.Count,
		hostnicMetrics.HostnicIpamPoolFailed.Node,
	)
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamNotFound,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamNotFound.Count,
		hostnicMetrics.HostnicIpamNotFound.Node,
	)
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamAllocFailed,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamAllocFailed.Count,
		hostnicMetrics.HostnicIpamAllocFailed.Node,
	)
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamFreeFromPoolFailed,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamFreeFromPoolFailed.Count,
		hostnicMetrics.HostnicIpamFreeFromPoolFailed.Node,
	)
	ch <- prometheus.MustNewConstMetric(
		c.HostnicIpamFreeFromHostFailed,
		prometheus.GaugeValue,
		hostnicMetrics.HostnicIpamFreeFromHostFailed.Count,
		hostnicMetrics.HostnicIpamFreeFromHostFailed.Node,
	)
}

func NewHostnicMetricsManager(kubeclient kubernetes.Interface, ipamclient ipam.IPAMClient, oddCount *OddPodCount) *HostnicMetricsManager {
	return &HostnicMetricsManager{
		kubeclient: kubeclient,
		ipamclient: ipamclient,
		oddCount:   oddCount,
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
		HostnicIpamVxnetAllocator: prometheus.NewDesc(
			"hostnic_ipam_vxnet_allocator",
			"describe vxnet ipam allocator in cluster with hostnic cni",
			[]string{"vxnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamVxnetUnallocator: prometheus.NewDesc(
			"hostnic_ipam_vxnet_unallocator",
			"describe vxnet ipam unallocator in cluster with hostnic cni",
			[]string{"vxnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamVxnetTotal: prometheus.NewDesc(
			"hostnic_ipam_vxnet_total",
			"describe vxnet ipam total in cluster with hostnic cni",
			[]string{"vxnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamSubnetAllocator: prometheus.NewDesc(
			"hostnic_ipam_subnet_allocator",
			"describe subnet ipam allocator in cluster with hostnic cni",
			[]string{"subnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamSubnetUnallocator: prometheus.NewDesc(
			"hostnic_ipam_subnet_unallocator",
			"describe subnet ipam unallocator in cluster with hostnic cni",
			[]string{"subnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamSubnetTotal: prometheus.NewDesc(
			"hostnic_ipam_subnet_total",
			"describe subnet ipam total in cluster with hostnic cni",
			[]string{"subnet_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamNamespaceAllocator: prometheus.NewDesc(
			"hostnic_ipam_namespace_allocator",
			"describe namespace ipam allocator in cluster with hostnic cni",
			[]string{"ns_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamNamespaceUnallocator: prometheus.NewDesc(
			"hostnic_ipam_namespace_unallocator",
			"describe namespace ipam unallocator in cluster with hostnic cni",
			[]string{"ns_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamNamespaceTotal: prometheus.NewDesc(
			"hostnic_ipam_namespace_total",
			"describe namespace ipam total in cluster with hostnic cni",
			[]string{"ns_name", "node_name"},
			prometheus.Labels{},
		),
		HostnicIpamBlockFailed: prometheus.NewDesc(
			"hostnic_ipam_alloc_from_block_failed",
			"describe ipam block failed in cluster with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
		HostnicIpamPoolFailed: prometheus.NewDesc(
			"hostnic_ipam_alloc_from_pool_failed",
			"describe ipam pool failed in cluster with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
		HostnicIpamNotFound: prometheus.NewDesc(
			"hostnic_ipam_alloc_resource_notfound",
			"describe ipam not found in cluster with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
		HostnicIpamAllocFailed: prometheus.NewDesc(
			"hostnic_ipam_alloc_from_host_failed",
			"describe ipam alloc failed in cluster with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
		HostnicIpamFreeFromPoolFailed: prometheus.NewDesc(
			"hostnic_ipam_free_from_pool_failed",
			"describe ipam free from pool failed in cluster with hostnic cni",
			[]string{"node_name"},
			prometheus.Labels{},
		),
		HostnicIpamFreeFromHostFailed: prometheus.NewDesc(
			"hostnic_ipam_free_from_host_failed",
			"describe ipam free from host failed in cluster with hostnic cni",
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

	// return a dummy name when no mapping rule found for this subnet
	return constants.MetricsDummyNamespaceForSubnet
}
