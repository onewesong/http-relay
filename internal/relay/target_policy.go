package relay

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrProhibitedTarget = errors.New("target URL resolves to a prohibited address")
	ErrTargetDNS        = errors.New("target URL DNS lookup failed")
)

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// TargetPolicy validates a client-selected upstream URL before it is relayed.
// A nil policy means no target-address restriction.
type TargetPolicy struct {
	resolver ipResolver
}

type targetDialInfo struct {
	host      string
	port      string
	addresses []net.IPAddr
}

type targetPolicyContextKey struct{}

func withTargetPolicy(ctx context.Context, policy *TargetPolicy) context.Context {
	return context.WithValue(ctx, targetPolicyContextKey{}, policy)
}

func targetPolicyFromContext(ctx context.Context) *TargetPolicy {
	policy, _ := ctx.Value(targetPolicyContextKey{}).(*TargetPolicy)
	return policy
}

func NewTargetPolicy() *TargetPolicy {
	return &TargetPolicy{resolver: net.DefaultResolver}
}

func (p *TargetPolicy) Validate(ctx context.Context, target *url.URL) error {
	_, err := p.resolve(ctx, target)
	return err
}

func (p *TargetPolicy) resolve(ctx context.Context, target *url.URL) (targetDialInfo, error) {
	if p == nil || target == nil {
		return targetDialInfo{}, nil
	}
	host := target.Hostname()
	if host == "" {
		return targetDialInfo{}, ErrProhibitedTarget
	}

	var addresses []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		resolver := p.resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		var err error
		addresses, err = resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return targetDialInfo{}, ErrTargetDNS
		}
	}

	for _, address := range addresses {
		if forbiddenTargetIP(address.IP) {
			return targetDialInfo{}, ErrProhibitedTarget
		}
	}
	port := target.Port()
	if port == "" {
		if strings.EqualFold(target.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return targetDialInfo{}, ErrProhibitedTarget
	}
	return targetDialInfo{host: host, port: port, addresses: addresses}, nil
}

func forbiddenTargetIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return true
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}
