package metrics

import (
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yunify/hostnic-cni/pkg/allocator"
)

type HostnicMetricsManager struct {
	HostnicVxnetCount    *prometheus.Desc
	HostnicVxnetPodCount *prometheus.Desc
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

func (c *HostnicMetricsManager) GenerateMetrics() (vxnetInfos []HostnicVxnetInfo, podInfos []HostnicVxnetPodInfo) {
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
	return hostnicVxnetInfos, hostnicVxnetPodInfos
}

func (c *HostnicMetricsManager) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.HostnicVxnetCount
	ch <- c.HostnicVxnetPodCount
}

func (c *HostnicMetricsManager) Collect(ch chan<- prometheus.Metric) {
	hostnicVxnetInfos, hostnicVxnetPodInfos := c.GenerateMetrics()
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
}

func NewHostnicMetricsManager() *HostnicMetricsManager {
	return &HostnicMetricsManager{
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
	}
}
