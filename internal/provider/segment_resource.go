package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"

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
	Address       types.String `tfsdk:"address"`
	Mask          types.String `tfsdk:"mask"`
	SecurityLevel types.String `tfsdk:"security_level"`
	Up            types.Bool   `tfsdk:"up"`
}

func (r *segmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_segment"
}

func (r *segmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Keenetic network segment: an `interface Bridge<N>` with an IPv4 gateway address. " +
			"The segment created here has no member ports, VLANs or Wi-Fi APs — attach those in the router UI or future resources.",
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
			"address": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Gateway IPv4 address of the segment, e.g. `192.168.90.1`.",
			},
			"mask": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("255.255.255.0"),
				MarkdownDescription: "Dotted IPv4 netmask. Defaults to `255.255.255.0`.",
			},
			"security_level": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("private"),
				MarkdownDescription: "Keenetic security level: `private` (LAN), `protected`, or `public` (WAN-facing).",
				Validators: []validator.String{
					stringvalidator.OneOf("private", "protected", "public"),
				},
			},
			"up": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the segment interface is administratively up. Defaults to `true`.",
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
	if err := r.client.ApplySegment(ctx, toSegment(plan)); err != nil {
		resp.Diagnostics.AddError("Create segment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.readInto(ctx, plan.Name.ValueString(), &plan)...)
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
		resp.State.RemoveResource(ctx) // drifted away (deleted out of band)
		return
	}
	applySegment(seg, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *segmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan segmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplySegment(ctx, toSegment(plan)); err != nil {
		resp.Diagnostics.AddError("Update segment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.readInto(ctx, plan.Name.ValueString(), &plan)...)
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
		return
	}
}

func (r *segmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// readInto refreshes model fields from the router after a write.
func (r *segmentResource) readInto(ctx context.Context, name string, m *segmentModel) (diags diag.Diagnostics) {
	seg, found, err := r.client.GetSegment(ctx, name)
	if err != nil {
		diags.AddError("Read-back failed", err.Error())
		return
	}
	if !found {
		diags.AddError("Segment missing after write", fmt.Sprintf("interface %q not found after apply", name))
		return
	}
	applySegment(seg, m)
	return
}

func toSegment(m segmentModel) client.Segment {
	return client.Segment{
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		Address:       m.Address.ValueString(),
		Mask:          m.Mask.ValueString(),
		SecurityLevel: m.SecurityLevel.ValueString(),
		Up:            m.Up.ValueBool(),
	}
}

// applySegment copies router state into the model, preserving the configured mask default
// when the router echoes it back.
func applySegment(seg *client.Segment, m *segmentModel) {
	m.Name = types.StringValue(seg.Name)
	m.Description = types.StringValue(seg.Description)
	m.Address = types.StringValue(seg.Address)
	mask := seg.Mask
	if mask == "" {
		mask = "255.255.255.0"
	}
	m.Mask = types.StringValue(mask)
	level := seg.SecurityLevel
	if level == "" {
		level = "private"
	}
	m.SecurityLevel = types.StringValue(level)
	m.Up = types.BoolValue(seg.Up)
}
