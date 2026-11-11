package client

import (
	"context"
	"strings"
)

// KnownClient is a registered device: a named host (`known host <name> <mac>`) that optionally has a
// reserved IP (`ip dhcp host <mac> <ip>`). The device is identified by its MAC.
type KnownClient struct {
	Name string
	MAC  string
	IP   string // static reservation; "" => no reservation
}

type rcKnownHost struct {
	MAC string `json:"mac"`
}

// knownByMAC returns the registered name for a MAC (""/false when none).
func knownByMAC(hosts map[string]rcKnownHost, mac string) (string, bool) {
	mac = strings.ToLower(mac)
	for name, h := range hosts {
		if strings.ToLower(h.MAC) == mac {
			return name, true
		}
	}
	return "", false
}

func leaseIP(leases []rcHost, mac string) (string, bool) {
	mac = strings.ToLower(mac)
	for _, l := range leases {
		if strings.ToLower(l.MAC) == mac {
			return l.IP, true
		}
	}
	return "", false
}

// GetClient returns the registered device for `mac`, or (nil,false,nil) if the MAC is not a known host.
func (c *Client) GetClient(ctx context.Context, mac string) (*KnownClient, bool, error) {
	var hosts map[string]rcKnownHost
	if err := c.get(ctx, "/rci/show/rc/known/host", &hosts); err != nil {
		return nil, false, err
	}
	name, ok := knownByMAC(hosts, mac)
	if !ok {
		return nil, false, nil
	}
	kc := &KnownClient{Name: name, MAC: strings.ToLower(mac)}
	var leases []rcHost
	if err := c.get(ctx, "/rci/show/rc/ip/dhcp/host", &leases); err != nil {
		return nil, false, err
	}
	if ip, has := leaseIP(leases, mac); has {
		kc.IP = ip
	}
	return kc, true, nil
}

// knownHostDelCmd deletes a known host BY MAC. This is deliberate: deleting by name alone
// (`known host <name> no`) wipes the ENTIRE known-host list on this firmware — only the mac-scoped
// form removes a single entry. Callers must confirm the MAC is present first.
func knownHostDelCmd(mac string) map[string]interface{} {
	return map[string]interface{}{"known": map[string]interface{}{"host": map[string]interface{}{"mac": strings.ToLower(mac), "no": true}}}
}

func knownHostSetCmd(name, mac string) map[string]interface{} {
	return map[string]interface{}{"known": map[string]interface{}{"host": map[string]interface{}{"name": name, "mac": strings.ToLower(mac)}}}
}

func dhcpHostSetCmd(mac, ip string) map[string]interface{} {
	return map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{"host": map[string]interface{}{"mac": strings.ToLower(mac), "ip": ip}}}}
}

func dhcpHostDelCmd(mac string) map[string]interface{} {
	return map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{"host": map[string]interface{}{"mac": strings.ToLower(mac), "no": true}}}}
}

// ApplyClient creates or updates the device (name + optional reserved IP), then saves. Idempotent.
func (c *Client) ApplyClient(ctx context.Context, kc KnownClient) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	mac := strings.ToLower(kc.MAC)

	var hosts map[string]rcKnownHost
	if err := c.get(ctx, "/rci/show/rc/known/host", &hosts); err != nil {
		return err
	}
	existing, present := knownByMAC(hosts, mac)

	var cmds []interface{}
	if present && existing != kc.Name {
		// Re-name: drop the old name entry for this MAC first (by MAC, so only it is removed).
		cmds = append(cmds, knownHostDelCmd(mac))
	}
	if !present || existing != kc.Name {
		cmds = append(cmds, knownHostSetCmd(kc.Name, mac))
	}

	var leases []rcHost
	if err := c.get(ctx, "/rci/show/rc/ip/dhcp/host", &leases); err != nil {
		return err
	}
	_, hasLease := leaseIP(leases, mac)
	if kc.IP != "" {
		cmds = append(cmds, dhcpHostSetCmd(mac, kc.IP))
	} else if hasLease {
		cmds = append(cmds, dhcpHostDelCmd(mac))
	}

	if len(cmds) > 0 {
		if _, err := c.post(ctx, cmds); err != nil {
			return err
		}
	}
	return c.save(ctx)
}

// DeleteClient removes the device's known-host entry and reserved IP (each only if present), then saves.
func (c *Client) DeleteClient(ctx context.Context, mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	mac = strings.ToLower(mac)

	var hosts map[string]rcKnownHost
	if err := c.get(ctx, "/rci/show/rc/known/host", &hosts); err != nil {
		return err
	}
	var cmds []interface{}
	if _, present := knownByMAC(hosts, mac); present {
		cmds = append(cmds, knownHostDelCmd(mac))
	}
	var leases []rcHost
	if err := c.get(ctx, "/rci/show/rc/ip/dhcp/host", &leases); err != nil {
		return err
	}
	if _, has := leaseIP(leases, mac); has {
		cmds = append(cmds, dhcpHostDelCmd(mac))
	}
	if len(cmds) > 0 {
		if _, err := c.post(ctx, cmds); err != nil {
			return err
		}
	}
	return c.save(ctx)
}
