package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nghminh163/terraform-provider-keenetic/internal/client"
)

var (
	_ resource.Resource                = (*segmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*segmentResource)(nil)
	_ resource.ResourceWithImportState = (*segmentResource)(nil)
)

var (
	regexBridge = regexp.MustCompile(`^Bridge\d+$`)
	regexAP2    = regexp.MustCompile(`^WifiMaster0/AccessPoint\d+$`)
	regexAP5    = regexp.MustCompile(`^WifiMaster1/AccessPoint\d+$`)
)

// wifiBandSchema builds the schema for one Wi-Fi band; apRegex pins it to the right radio's slots.
func wifiBandSchema(band string, apRegex *regexp.Regexp, example string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "The " + band + " access point for this segment.",
		Attributes: map[string]schema.Attribute{
			"access_point": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Spare access-point slot for " + band + ", e.g. `" + example + "`.",
				Validators:          []validator.String{stringvalidator.RegexMatches(apRegex, "must be a "+band+" access point like \""+example+"\"")},
			},
			"ssid": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Network name (SSID) this AP broadcasts.",
			},
			"psk": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Pre-shared key. Required unless `security = \"open\"`; keep it out of committed config (env/secret store).",
			},
			"security": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("wpa2-psk"),
				MarkdownDescription: "`wpa2-psk` (default) | `wpa-wpa2-psk` | `wpa-psk` | `open`.",
				Validators:          []validator.String{stringvalidator.OneOf("wpa2-psk", "wpa-wpa2-psk", "wpa-psk", "open")},
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Broadcast (AP up). `false` keeps the AP configured but down. Default `true`.",
			},
		},
	}
}

// NewSegmentResource is the factory registered with the provider.
func NewSegmentResource() resource.Resource {
	return &segmentResource{}
}

type segmentResource struct {
	client *client.Client
}

type segmentModel struct {
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	SecurityLevel types.String `tfsdk:"security_level"`
	Up            types.Bool   `tfsdk:"up"`
	IPv4          *ipv4Model     `tfsdk:"ipv4"`
	Wireless      *wirelessModel `tfsdk:"wireless"`
	VLAN          *vlanModel     `tfsdk:"vlan"`
}

type ipv4Model struct {
	Address types.String `tfsdk:"address"`
	Mask    types.String `tfsdk:"mask"`
	DHCP    *dhcpModel   `tfsdk:"dhcp"`
}

type dhcpModel struct {
	Enabled    types.Bool   `tfsdk:"enabled"`
	RangeStart types.String `tfsdk:"range_start"`
	RangeSize  types.Int64  `tfsdk:"range_size"`
	RangeEnd   types.String `tfsdk:"range_end"`
	DNSServers types.List   `tfsdk:"dns_servers"`
	LeaseTime  types.Int64  `tfsdk:"lease_time"`
}

type wirelessModel struct {
	Band2 *wifiBandModel `tfsdk:"band_2ghz"`
	Band5 *wifiBandModel `tfsdk:"band_5ghz"`
}

type wifiBandModel struct {
	AccessPoint types.String `tfsdk:"access_point"`
	SSID        types.String `tfsdk:"ssid"`
	PSK         types.String `tfsdk:"psk"`
	Security    types.String `tfsdk:"security"`
	Enabled     types.Bool   `tfsdk:"enabled"`
}

type vlanModel struct {
	ID    types.Int64 `tfsdk:"id"`
	Ports types.List  `tfsdk:"ports"`
}

func (r *segmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_segment"
}

func (r *segmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Keenetic network segment: an `interface Bridge<N>` with IPv4 settings and an " +
			"optional bound DHCP pool. Created with no member ports/VLANs/Wi-Fi APs (attach those separately).",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Interface id of the segment, e.g. `Bridge2`. Changing it replaces the segment.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexBridge, "must be a bridge interface id like \"Bridge2\""),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Human-readable segment name shown in the router UI.",
			},
			"security_level": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("private"),
				MarkdownDescription: "`private` (LAN) | `protected` | `public` (WAN). Default `private`.",
				Validators:          []validator.String{stringvalidator.OneOf("private", "protected", "public")},
			},
			"up": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Interface administratively up. Default `true`.",
			},
			"wireless": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Wi-Fi access points bound to this segment, up to one per band. Each uses a spare " +
					"`WifiMaster<n>/AccessPoint<m>` slot (0/1 are the router's own networks) and is included in the " +
					"segment's bridge.",
				Attributes: map[string]schema.Attribute{
					"band_2ghz": wifiBandSchema("2.4 GHz", regexAP2, "WifiMaster0/AccessPoint2"),
					"band_5ghz": wifiBandSchema("5 GHz", regexAP5, "WifiMaster1/AccessPoint2"),
				},
			},
			"vlan": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "802.1Q VLAN carried by this segment: a VLAN id tagged on one or more physical " +
					"ports (each becomes a `<port>/Vlan<id>` sub-interface included in the bridge). Applying a VLAN id " +
					"already carried by another segment fails with a conflict error.",
				Attributes: map[string]schema.Attribute{
					"id": schema.Int64Attribute{
						Required:            true,
						MarkdownDescription: "VLAN id, 1–4094.",
						Validators:          []validator.Int64{int64validator.Between(1, 4094)},
					},
					"ports": schema.ListAttribute{
						Required:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Physical ports to tag, e.g. `[\"GigabitEthernet0\", \"GigabitEthernet1\"]`.",
					},
				},
			},
			"ipv4": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "IPv4 settings for the segment.",
				Attributes: map[string]schema.Attribute{
					"address": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Segment gateway IPv4 address, e.g. `192.168.90.1`.",
					},
					"mask": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString("255.255.255.0"),
						MarkdownDescription: "Dotted IPv4 netmask. Default `255.255.255.0`.",
					},
					"dhcp": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "DHCP pool bound to this segment. Omit to run the segment without DHCP.",
						Attributes: map[string]schema.Attribute{
							"enabled": schema.BoolAttribute{
								Optional:            true,
								Computed:            true,
								Default:             booldefault.StaticBool(true),
								MarkdownDescription: "Whether the pool serves leases. Default `true`.",
							},
							"range_start": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "First address of the pool, e.g. `192.168.90.33`.",
							},
							"range_size": schema.Int64Attribute{
								Optional:            true,
								MarkdownDescription: "Number of addresses in the pool (the last address is derived).",
							},
							"range_end": schema.StringAttribute{
								Computed:            true,
								MarkdownDescription: "Last address of the pool, derived from `range_start` + `range_size`.",
							},
							"dns_servers": schema.ListAttribute{
								Optional:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Up to two DNS servers handed to clients. Omit to use the router's own resolver.",
							},
							"lease_time": schema.Int64Attribute{
								Optional:            true,
								Computed:            true,
								Default:             int64default.StaticInt64(86400),
								MarkdownDescription: "Lease time in seconds. Default `86400` (24h).",
							},
						},
					},
				},
			},
		},
	}
}

func (r *segmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *segmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan segmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seg, diags := toSegment(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplySegment(ctx, seg); err != nil {
		resp.Diagnostics.AddError("Create segment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.refresh(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *segmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state segmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seg, found, err := r.client.GetSegment(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read segment failed", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(applySegment(ctx, seg, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *segmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan segmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seg, diags := toSegment(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplySegment(ctx, seg); err != nil {
		resp.Diagnostics.AddError("Update segment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.refresh(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *segmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state segmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSegment(ctx, state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete segment failed", err.Error())
	}
}

func (r *segmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// refresh reads the router back into the model after a write.
func (r *segmentResource) refresh(ctx context.Context, m *segmentModel) diag.Diagnostics {
	seg, found, err := r.client.GetSegment(ctx, m.Name.ValueString())
	if err != nil {
		var d diag.Diagnostics
		d.AddError("Read-back failed", err.Error())
		return d
	}
	if !found {
		var d diag.Diagnostics
		d.AddError("Segment missing after write", fmt.Sprintf("interface %q not found after apply", m.Name.ValueString()))
		return d
	}
	return applySegment(ctx, seg, m)
}

// toSegment converts the plan model to a client.Segment, applying field defaults.
func toSegment(ctx context.Context, m segmentModel) (client.Segment, diag.Diagnostics) {
	var diags diag.Diagnostics
	seg := client.Segment{
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		SecurityLevel: m.SecurityLevel.ValueString(),
		Up:            m.Up.ValueBool(),
	}
	if m.IPv4 != nil {
		seg.Address = m.IPv4.Address.ValueString()
		seg.Mask = m.IPv4.Mask.ValueString()
		if d := m.IPv4.DHCP; d != nil {
			dhcp := client.DHCP{
				Enabled:    d.Enabled.ValueBool(),
				RangeStart: d.RangeStart.ValueString(),
				RangeSize:  int(d.RangeSize.ValueInt64()),
				LeaseTime:  int(d.LeaseTime.ValueInt64()),
			}
			if !d.DNSServers.IsNull() && !d.DNSServers.IsUnknown() {
				var servers []string
				diags.Append(d.DNSServers.ElementsAs(ctx, &servers, false)...)
				dhcp.DNSServers = servers
			}
			seg.DHCP = &dhcp
		}
	}
	if w := m.Wireless; w != nil {
		wl := &client.Wireless{}
		if w.Band2 != nil {
			wl.Band2 = toBand(w.Band2)
		}
		if w.Band5 != nil {
			wl.Band5 = toBand(w.Band5)
		}
		seg.Wireless = wl
	}
	if v := m.VLAN; v != nil {
		vl := &client.VLAN{ID: int(v.ID.ValueInt64())}
		if !v.Ports.IsNull() && !v.Ports.IsUnknown() {
			var ports []string
			diags.Append(v.Ports.ElementsAs(ctx, &ports, false)...)
			vl.Ports = ports
		}
		seg.VLAN = vl
	}
	return seg, diags
}

func toBand(b *wifiBandModel) *client.WifiBand {
	return &client.WifiBand{
		AccessPoint: b.AccessPoint.ValueString(),
		SSID:        b.SSID.ValueString(),
		PSK:         b.PSK.ValueString(),
		Security:    b.Security.ValueString(),
		Enabled:     b.Enabled.ValueBool(),
	}
}

func fromBand(b *client.WifiBand) *wifiBandModel {
	return &wifiBandModel{
		AccessPoint: types.StringValue(b.AccessPoint),
		SSID:        types.StringValue(b.SSID),
		PSK:         stringOrNull(b.PSK),
		Security:    types.StringValue(b.Security),
		Enabled:     types.BoolValue(b.Enabled),
	}
}

// applySegment copies router state into the model.
func applySegment(ctx context.Context, seg *client.Segment, m *segmentModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.Name = types.StringValue(seg.Name)
	m.Description = types.StringValue(seg.Description)
	m.SecurityLevel = types.StringValue(orDefault(seg.SecurityLevel, "private"))
	m.Up = types.BoolValue(seg.Up)

	ipv4 := &ipv4Model{
		Address: types.StringValue(seg.Address),
		Mask:    types.StringValue(orDefault(seg.Mask, "255.255.255.0")),
	}
	if seg.DHCP != nil {
		d := seg.DHCP
		servers := types.ListNull(types.StringType)
		if len(d.DNSServers) > 0 {
			list, ld := types.ListValueFrom(ctx, types.StringType, d.DNSServers)
			diags.Append(ld...)
			servers = list
		}
		ipv4.DHCP = &dhcpModel{
			Enabled:    types.BoolValue(d.Enabled),
			RangeStart: stringOrNull(d.RangeStart),
			RangeSize:  types.Int64Value(int64(d.RangeSize)),
			RangeEnd:   stringOrNull(d.RangeEnd),
			DNSServers: servers,
			LeaseTime:  types.Int64Value(int64(d.LeaseTime)),
		}
	}
	m.IPv4 = ipv4

	if seg.Wireless != nil {
		wm := &wirelessModel{}
		if seg.Wireless.Band2 != nil {
			wm.Band2 = fromBand(seg.Wireless.Band2)
		}
		if seg.Wireless.Band5 != nil {
			wm.Band5 = fromBand(seg.Wireless.Band5)
		}
		m.Wireless = wm
	} else {
		m.Wireless = nil
	}

	if seg.VLAN != nil {
		ports, pd := types.ListValueFrom(ctx, types.StringType, seg.VLAN.Ports)
		diags.Append(pd...)
		m.VLAN = &vlanModel{ID: types.Int64Value(int64(seg.VLAN.ID)), Ports: ports}
	} else {
		m.VLAN = nil
	}
	return diags
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}
