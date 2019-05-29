package main

import (
	"fmt"
	"net"
)

func getVPNNet(ip string) string {
	addr := &net.IPNet{
		IP:   net.ParseIP(ip),
		Mask: net.IPv4Mask(255, 255, 255, 255),
	}
	i := addr.IP.To4()
	fmt.Printf("%+v", addr)
	i[2] = 255
	i[3] = 254
	addr.IP = i
	fmt.Printf("%+v", addr)
	return addr.String()
}

func main() {
	fmt.Println(getVPNNet("192.168.68.2"))
}
