package client

import (
	"context"
	"strings"
)

// StaticLease is one DHCP reservation (a global `ip dhcp host` entry: the router keeps a single flat
// list across all pools, matched to a pool by which subnet the IP falls in).
type StaticLease struct {
	MAC string // lowercase, e.g. "de:ad:be:ef:00:01"
	IP  string
}

type rcHost struct {
	MAC string `json:"mac"`
	IP  string `json:"ip"`
}

// GetStaticLease returns the reservation for mac, or (nil,false,nil) if none exists.
func (c *Client) GetStaticLease(ctx context.Context, mac string) (*StaticLease, bool, error) {
	var hosts []rcHost
	if err := c.get(ctx, "/rci/show/rc/ip/dhcp/host", &hosts); err != nil {
		return nil, false, err
	}
	mac = strings.ToLower(mac)
	for _, h := range hosts {
		if strings.ToLower(h.MAC) == mac {
			return &StaticLease{MAC: h.MAC, IP: h.IP}, true, nil
		}
	}
	return nil, false, nil
}

// ApplyStaticLease creates or updates the reservation for the given MAC, then saves. Idempotent.
func (c *Client) ApplyStaticLease(ctx context.Context, l StaticLease) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := []interface{}{
		map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{
			"host": map[string]interface{}{"mac": strings.ToLower(l.MAC), "ip": l.IP},
		}}},
	}
	if _, err := c.post(ctx, cmd); err != nil {
		return err
	}
	return c.save(ctx)
}

// DeleteStaticLease removes the reservation for the given MAC, then saves.
func (c *Client) DeleteStaticLease(ctx context.Context, mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := []interface{}{
		map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{
			"host": map[string]interface{}{"mac": strings.ToLower(mac), "no": true},
		}}},
	}
	if _, err := c.post(ctx, cmd); err != nil {
		return err
	}
	return c.save(ctx)
}
