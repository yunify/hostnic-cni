package networkutils

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
)

type stringWriteCloser interface {
	io.Closer
	WriteString(s string) (int, error)
}

// GetVPNNet return the ip from the vpn tunnel, which in most time is the x.x.255.254
func GetVPNNet(ip string) string {
	i := net.ParseIP(ip).To4()
	i[2] = 255
	i[3] = 254
	addr := &net.IPNet{
		IP:   i,
		Mask: net.IPv4Mask(255, 255, 255, 255),
	}
	return addr.String()
}

// incrementIPv4Addr returns incremented IPv4 address
func incrementIPv4Addr(ip net.IP) (net.IP, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("%q is not a valid IPv4 Address", ip)
	}
	intIP := binary.BigEndian.Uint32([]byte(ip4))
	if intIP == (1<<32 - 1) {
		return nil, fmt.Errorf("%q will be overflowed", ip)
	}
	intIP++
	bytes := make([]byte, 4)
	binary.BigEndian.PutUint32(bytes, intIP)
	return net.IP(bytes), nil
}

func setProcSysByWritingFile(key, value string) error {
	f, err := os.OpenFile(key, os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	if err != nil {
		// If the write failed, just close
		_ = f.Close()
		return err
	}
	return f.Close()
}