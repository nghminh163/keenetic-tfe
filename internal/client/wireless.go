package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// WifiBand is one radio's access point bound to a segment: the provider configures a spare
// WifiMaster<n>/AccessPoint<m> slot and includes it in the segment's bridge. 2.4 GHz lives on
// WifiMaster0/*, 5 GHz on WifiMaster1/*.
type WifiBand struct {
	AccessPoint string // interface id, e.g. "WifiMaster0/AccessPoint2"
	SSID        string
	PSK         string // pre-shared key (sensitive); empty for an open network
	Security    string // "wpa2-psk" | "wpa-wpa2-psk" | "wpa-psk" | "open"
	Enabled     bool   // AP administratively up and broadcasting
}

// Wireless is the optional Wi-Fi configuration of a segment, up to one AP per band.
type Wireless struct {
	Band2 *WifiBand // 2.4 GHz (WifiMaster0/*)
	Band5 *WifiBand // 5 GHz  (WifiMaster1/*)
}

type rcAccessPoint struct {
	SSID           string `json:"ssid"`
	Up             bool   `json:"up"`
	Authentication struct {
		WPAPSK struct {
			PSK string `json:"psk"`
		} `json:"wpa-psk"`
	} `json:"authentication"`
	Encryption struct {
		Enable bool `json:"enable"`
		WPA    bool `json:"wpa"`
		WPA2   bool `json:"wpa2"`
		WPA3   bool `json:"wpa3"`
	} `json:"encryption"`
}

// masterOf returns the WifiMaster prefix ("WifiMaster0") of an access point id.
func masterOf(ap string) string {
	if i := strings.IndexByte(ap, '/'); i > 0 {
		return ap[:i]
	}
	return ap
}

func encryptionCmd(security string) map[string]interface{} {
	switch security {
	case "open":
		return map[string]interface{}{"enable": false}
	case "wpa-wpa2-psk":
		return map[string]interface{}{"enable": true, "wpa": true, "wpa2": true}
	case "wpa-psk":
		return map[string]interface{}{"enable": true, "wpa": true}
	default: // wpa2-psk
		return map[string]interface{}{"enable": true, "wpa2": true}
	}
}

func securityFromEncryption(e rcAccessPoint) string {
	switch {
	case !e.Encryption.Enable:
		return "open"
	case e.Encryption.WPA && e.Encryption.WPA2:
		return "wpa-wpa2-psk"
	case e.Encryption.WPA2:
		return "wpa2-psk"
	case e.Encryption.WPA:
		return "wpa-psk"
	default:
		return "wpa2-psk"
	}
}

// apConfigCmd writes an access point's SSID/security/PSK and up state. `up` follows Enabled; bridge
// membership is handled separately by bridgeIncludeCmd.
func apConfigCmd(b WifiBand) map[string]interface{} {
	ap := map[string]interface{}{
		"name":           b.AccessPoint,
		"ssid":           b.SSID,
		"security-level": map[string]bool{"private": true},
		"encryption":     encryptionCmd(b.Security),
		"up":             b.Enabled,
	}
	if b.Security != "open" && b.PSK != "" {
		ap["authentication"] = map[string]interface{}{"wpa-psk": map[string]interface{}{"psk": b.PSK}}
	}
	return map[string]interface{}{"interface": ap}
}

// bridgeIncludeCmd adds (or, with add=false, removes) an access point as a member of the bridge.
func bridgeIncludeCmd(bridge, ap string, add bool) map[string]interface{} {
	inc := map[string]interface{}{"interface": ap}
	if !add {
		inc["no"] = true
	}
	return map[string]interface{}{"interface": map[string]interface{}{"name": bridge, "include": inc}}
}

// apClearCmds returns the AP to its spare state: down, no encryption, no SSID.
func apClearCmds(ap string) []interface{} {
	return []interface{}{
		map[string]interface{}{"interface": map[string]interface{}{"name": ap, "up": false}},
		map[string]interface{}{"interface": map[string]interface{}{"name": ap, "encryption": map[string]interface{}{"enable": false}}},
		map[string]interface{}{"interface": map[string]interface{}{"name": ap, "ssid": ""}},
	}
}

// wifiMembers returns the WifiMaster*/AccessPoint* interfaces currently included in the bridge.
func wifiMembers(all map[string]json.RawMessage, bridge string) ([]string, error) {
	raw, ok := all[bridge]
	if !ok {
		return nil, nil
	}
	var def struct {
		Include []struct {
			Interface string `json:"interface"`
		} `json:"include"`
	}
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("decode %q include: %w", bridge, err)
	}
	var out []string
	for _, m := range def.Include {
		if strings.HasPrefix(m.Interface, "WifiMaster") {
			out = append(out, m.Interface)
		}
	}
	return out, nil
}

// readWireless builds the Wireless view of a segment from the interface map: the bridge's
// WifiMaster members on master 0 → Band2, master 1 → Band5 (first of each).
func readWireless(all map[string]json.RawMessage, bridge string) (*Wireless, error) {
	members, err := wifiMembers(all, bridge)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}
	w := &Wireless{}
	for _, ap := range members {
		raw, ok := all[ap]
		if !ok {
			continue
		}
		var def rcAccessPoint
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, fmt.Errorf("decode access point %q: %w", ap, err)
		}
		band := &WifiBand{
			AccessPoint: ap,
			SSID:        def.SSID,
			PSK:         def.Authentication.WPAPSK.PSK,
			Security:    securityFromEncryption(def),
			Enabled:     def.Up,
		}
		switch masterOf(ap) {
		case "WifiMaster0":
			if w.Band2 == nil {
				w.Band2 = band
			}
		case "WifiMaster1":
			if w.Band5 == nil {
				w.Band5 = band
			}
		}
	}
	if w.Band2 == nil && w.Band5 == nil {
		return nil, nil
	}
	return w, nil
}

// reconcileWireless (called with the lock held, after the bridge exists) makes the bridge's Wi-Fi
// membership match the desired Wireless: it disables any currently-included WifiMaster AP that the
// desired config does not keep, then configures and includes each desired band.
func (c *Client) reconcileWireless(ctx context.Context, bridge string, desired *Wireless) error {
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return err
	}
	current, err := wifiMembers(all, bridge)
	if err != nil {
		return err
	}

	keep := map[string]bool{}
	var bands []WifiBand
	if desired != nil {
		for _, b := range []*WifiBand{desired.Band2, desired.Band5} {
			if b != nil {
				keep[b.AccessPoint] = true
				bands = append(bands, *b)
			}
		}
	}

	// Remove + clear members we no longer want.
	for _, ap := range current {
		if keep[ap] {
			continue
		}
		cmds := []interface{}{bridgeIncludeCmd(bridge, ap, false)}
		cmds = append(cmds, apClearCmds(ap)...)
		if _, err := c.post(ctx, cmds); err != nil {
			return err
		}
	}

	// Configure + include desired bands.
	for _, b := range bands {
		if _, err := c.post(ctx, []interface{}{apConfigCmd(b)}); err != nil {
			return err
		}
		if _, err := c.post(ctx, []interface{}{bridgeIncludeCmd(bridge, b.AccessPoint, true)}); err != nil {
			return err
		}
	}
	return nil
}

// teardownWireless (lock held) removes and clears every WifiMaster AP of the bridge, before the
// bridge itself is deleted.
func (c *Client) teardownWireless(ctx context.Context, bridge string) error {
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return err
	}
	members, err := wifiMembers(all, bridge)
	if err != nil {
		return err
	}
	for _, ap := range members {
		cmds := []interface{}{bridgeIncludeCmd(bridge, ap, false)}
		cmds = append(cmds, apClearCmds(ap)...)
		if _, err := c.post(ctx, cmds); err != nil {
			return err
		}
	}
	return nil
}
