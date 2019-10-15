package main

import (
	"fmt"

	"github.com/yunify/hostnic-cni/pkg/qcclient"
)

func main() {
	client, err := qcclient.NewQingCloudClient(nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	tag, err := client.GetTagByLabel("K8S-Cluster-hostnic-test")
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Printf("%+v", tag)
}
