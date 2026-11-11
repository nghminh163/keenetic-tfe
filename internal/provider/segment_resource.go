package provider

import (
	"context"
	"fmt"
	"regexp"

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

var regexBridge = regexp.MustCompile(`^Bridge\d+$`)

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
	IPv4          *ipv4Model   `tfsdk:"ipv4"`
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
	return seg, diags
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
