package runs

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"net"
)

// A task-home run has its own internal network. Left to Docker, each takes
// a whole block from the daemon's default address pools, which a host runs
// out of after a few dozen networks. A run's network holds three
// containers, so each is given a /29 from one range set aside for runs.

// DefaultPrivateSubnetBase is the range run networks are cut from.
const DefaultPrivateSubnetBase = "10.213.0.0/16"

const privateSubnetBits = 29

// privateSubnets describes the /29 blocks inside a base range.
type privateSubnets struct {
	base  uint32
	count uint32
}

func parsePrivateSubnetBase(cidr string) (privateSubnets, error) {
	if cidr == "" {
		cidr = DefaultPrivateSubnetBase
	}
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return privateSubnets{}, fmt.Errorf("run subnet base %q is not an IPv4 range", cidr)
	}
	ones, _ := network.Mask.Size()
	if ones < 8 || ones > 24 {
		return privateSubnets{}, fmt.Errorf("run subnet base %q must be between /8 and /24", cidr)
	}
	if !network.IP.IsPrivate() {
		return privateSubnets{}, fmt.Errorf("run subnet base %q is not a private range", cidr)
	}
	return privateSubnets{base: binary.BigEndian.Uint32(network.IP.To4()), count: 1 << (privateSubnetBits - ones)}, nil
}

func (p privateSubnets) at(index uint32) string {
	var ip [4]byte
	binary.BigEndian.PutUint32(ip[:], p.base+(index%p.count)<<(32-privateSubnetBits))
	return fmt.Sprintf("%s/%d", net.IP(ip[:]), privateSubnetBits)
}

// allocate returns a block no unfinished run holds. Each pod starts at its
// own offset in the range, so two pods on one host rarely meet.
func (p privateSubnets) allocate(podNS string, used map[string]bool) (string, error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(podNS))
	start := h.Sum32() % p.count
	for i := uint32(0); i < p.count; i++ {
		if subnet := p.at(start + i); !used[subnet] {
			return subnet, nil
		}
	}
	return "", fmt.Errorf("no free run subnet")
}

// usedPrivateSubnets lists the blocks held by unfinished runs.
func (s *Supervisor) usedPrivateSubnets() (map[string]bool, error) {
	recs, err := s.ledger.All()
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, r := range recs {
		if !r.State.Terminal() && r.PrivateSubnet != "" {
			used[r.PrivateSubnet] = true
		}
	}
	return used, nil
}
