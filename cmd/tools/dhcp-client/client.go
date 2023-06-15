package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/client6"
	"github.com/insomniacslk/dhcp/netboot"
	"github.com/vishvananda/netlink"
)

var (
	ver     = flag.Int("v", 4, "IP version to use")
	ifname  = flag.String("i", "eth0", "Interface name")
	dryrun  = flag.Bool("dryrun", false, "Do not change network configuration")
	debug   = flag.Bool("d", false, "Print debug output")
	retries = flag.Int("r", 3, "Number of retries before giving up")
	noIfup  = flag.Bool("noifup", false, "If set, don't wait for the interface to be up")
)

func main() {

	flag.Parse()
	if len(os.Args) < 2 {
		fmt.Println("please use -h to get some help!")
		return
	}

	var (
		err      error
		bootconf *netboot.BootConf
	)
	// bring interface up
	if !*noIfup {
		_, err = netboot.IfUp(*ifname, 5*time.Second)
		if err != nil {
			log.Fatalf("failed to bring interface %s up: %v", *ifname, err)
		}
	}
	if *ver == 6 {
		bootconf, err = dhclient6(*ifname, *retries+1, *debug)
	} else {
		bootconf, err = dhclient4(*ifname, *retries+1, *debug)
	}
	if err != nil {
		log.Fatal(err)
	}
	// configure the interface
	log.Printf("Setting network configuration:")
	log.Printf("%+v", bootconf)
	if *dryrun {
		log.Printf("dry run requested, not changing network configuration")
	} else {
		// add ip to intf
		la := netlink.NewLinkAttrs()
		la.Name = *ifname
		br := &netlink.Bridge{LinkAttrs: la}

		for _, addrConf := range bootconf.Addresses {
			addr := &netlink.Addr{
				IPNet:       &addrConf.IPNet,
				ValidLft:    int(addrConf.ValidLifetime.Seconds()),
				PreferedLft: int(addrConf.ValidLifetime.Seconds()),
			}
			err = netlink.AddrReplace(br, addr)
			if err != nil {
				log.Printf("replace addr %+v to intf %s error: %v", addr, *ifname, err)
			} else {
				log.Printf("replace addr %+v to intf %s success", addr, *ifname)
			}
		}
	}
}

func dhclient6(ifname string, attempts int, verbose bool) (*netboot.BootConf, error) {
	if attempts < 1 {
		attempts = 1
	}
	llAddr, err := dhcpv6.GetLinkLocalAddr(ifname)
	if err != nil {
		return nil, err
	}
	laddr := net.UDPAddr{
		IP:   llAddr,
		Port: dhcpv6.DefaultClientPort,
		Zone: ifname,
	}
	raddr := net.UDPAddr{
		IP:   dhcpv6.AllDHCPRelayAgentsAndServers,
		Port: dhcpv6.DefaultServerPort,
		Zone: ifname,
	}
	c := client6.NewClient()
	c.LocalAddr = &laddr
	c.RemoteAddr = &raddr
	var conv []dhcpv6.DHCPv6
	for attempt := 0; attempt < attempts; attempt++ {
		log.Printf("Attempt %d of %d", attempt+1, attempts)
		conv, err = c.Exchange(ifname, dhcpv6.WithNetboot)
		if err != nil && attempt < attempts {
			log.Printf("Error: %v", err)
			continue
		}
		break
	}
	if verbose {
		for _, m := range conv {
			log.Print(m.Summary())
		}
	}
	if err != nil {
		return nil, err
	}
	// extract the network configuration
	netconf, err := netboot.ConversationToNetconf(conv)
	return netconf, err
}

func dhclient4(ifname string, attempts int, verbose bool) (*netboot.BootConf, error) {
	if attempts < 1 {
		attempts = 1
	}
	client := client4.NewClient()
	var (
		conv []*dhcpv4.DHCPv4
		err  error
	)
	for attempt := 0; attempt < attempts; attempt++ {
		log.Printf("Attempt %d of %d", attempt+1, attempts)
		conv, err = client.Exchange(ifname)
		if err != nil && attempt < attempts {
			log.Printf("Error: %v", err)
			continue
		}
		break
	}
	if verbose {
		for _, m := range conv {
			log.Print(m.Summary())
		}
	}
	if err != nil {
		return nil, err
	}
	// extract the network configuration
	netconf, err := netboot.ConversationToNetconfv4(conv)
	return netconf, err
}
