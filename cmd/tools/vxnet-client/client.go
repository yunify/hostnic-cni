package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

func showVxNetInfo(vxnet string) *rpc.VxNet {
	if vxnets, err := qcclient.QClient.GetVxNets([]string{vxnet}); err != nil {
		fmt.Printf("Get info for vxnet %s failed: %v\n", vxnet, err)
	} else {
		if len(vxnets) == 0 {
			fmt.Printf("VxNet %s: not found\n", vxnet)
		} else {
			fmt.Printf("VxNet %s: %v\n", vxnet, vxnets[vxnet])
			return vxnets[vxnet]
		}
	}
	return nil
}

func deleteVIPs(vxnet *rpc.VxNet) {
	for {
		if vips, err := qcclient.QClient.DescribeVIPs(vxnet); err != nil {
			fmt.Printf("Get vips for vxnet %s failed: %v\n", vxnet.ID, err)
			return
		} else {
			if len(vips) == 0 {
				break
			}
			var vipsToDel []string
			for _, vip := range vips {
				vipsToDel = append(vipsToDel, vip.ID)
			}
			if job, err := qcclient.QClient.DeleteVIPs(vipsToDel); err != nil {
				fmt.Printf("Clear vips for VxNet %s failed: %v\n", vxnet.ID, err)
				return
			} else {
				fmt.Printf("Clear vips for VxNet %s: count %d job id %s\n", vxnet.ID, len(vips), job)
			}
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("Clear vips for VxNet %s success\n", vxnet.ID)
}

var vxnet string
var clear bool

func main() {
	flag.StringVar(&vxnet, "vxnet", "", "show vxnet info")
	flag.BoolVar(&clear, "clear-vips", false, "clear vips for vxnet")
	flag.Parse()

	if vxnet == "" {
		fmt.Printf("Plesse input vxnet: ./vxnet-client --vxnet vxnet-xxxxxxxx\n")
		return
	}

	qcclient.SetupQingCloudClient(qcclient.Options{})
	if v := showVxNetInfo(vxnet); v != nil && clear == true {
		deleteVIPs(v)
	}
}
