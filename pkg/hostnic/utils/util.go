package utils

import (
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"math/big"
	"net"
	"time"
)

var RetryConf = wait.Backoff{
	Steps:    constants.RetrySteps,
	Duration: 10 * time.Millisecond,
	Factor:   1.0,
	Jitter:   0.1,
}

func IPRangeCount(from, to string) int {
	startIP := cnet.ParseIP(from)
	endIP := cnet.ParseIP(to)
	startInt := cnet.IPToBigInt(*startIP)
	endInt := cnet.IPToBigInt(*endIP)
	return int(big.NewInt(0).Sub(endInt, startInt).Int64() + 1)
}

func GetIPsFromRange(from, to string) []string {
	count := IPRangeCount(from, to)
	result := []string{}
	for i := 0; i < count; i++ {
		newIP := IncrementIP(*ParseIP(from), big.NewInt(int64(i)))
		result = append(result, newIP.String())
	}
	return result
}

// ParseIP returns an IP from a string
func ParseIP(ip string) *net.IP {
	addr := net.ParseIP(ip)
	if addr == nil {
		return nil
	}
	// Always return IPv4 values as 4-bytes to be consistent with IPv4 IPNet
	// representations.
	if addr4 := addr.To4(); addr4 != nil {
		addr = addr4
	}
	return &addr
}

func IPToBigInt(ip net.IP) *big.Int {
	if ip.To4() != nil {
		return big.NewInt(0).SetBytes(ip.To4())
	} else {
		return big.NewInt(0).SetBytes(ip.To16())
	}
}

func BigIntToIP(ipInt *big.Int) net.IP {
	ip := net.IP(ipInt.Bytes())
	if ip.To4() != nil {
		return ip
	}
	a := ipInt.FillBytes(make([]byte, 16))
	return a
}

func IncrementIP(ip net.IP, increment *big.Int) net.IP {
	sum := big.NewInt(0).Add(IPToBigInt(ip), increment)
	return BigIntToIP(sum)
}
