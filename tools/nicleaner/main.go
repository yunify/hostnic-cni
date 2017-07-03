package main

import (
	"flag"
	"github.com/yunify/hostnic-cni/pkg"
	"os"
	"fmt"
	"github.com/yunify/hostnic-cni/provider"
	_ "github.com/yunify/hostnic-cni/provider/qingcloud"
)

var(
	cniConfig = "/etc/cni/net.d/10-hostnic.conf"
)

func init() {
	flag.StringVar(&cniConfig, "cni_config", cniConfig, "the hostnic cni config file.")
}

func clean(n *pkg.NetConf) (error){
	nicProvider, err := provider.New(n.Provider, n.Args)
	if err != nil {
		return err
	}
	for _, vxnetID := range nicProvider.GetVxNets(){
		nics, err := nicProvider.GetNicsUnderCurNamesp(&vxnetID)
		if err != nil {
			return err
		}
		for _, nic := range nics {
			err := nicProvider.DeleteNic(nic.ID)
			if err != nil {
				fmt.Printf("Delete nic %s error: %s\n", nic, err)
			}
		}
	}
	return nil
}

func main() {
	flag.Parse()
	netConf, err := pkg.LoadNetConfFromFile(cniConfig)
	if err != nil {
		fmt.Println("Load net conf error:", err.Error())
		os.Exit(1)
	}
	err = clean(netConf)
	if err != nil {
		fmt.Println("Clean nic error:", err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
