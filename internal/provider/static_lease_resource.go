package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nghminh163/terraform-provider-keenetic/internal/client"
)

var (
	_ resource.Resource                = (*staticLeaseResource)(nil)
	_ resource.ResourceWithConfigure   = (*staticLeaseResource)(nil)
	_ resource.ResourceWithImportState = (*staticLeaseResource)(nil)
)

var regexMAC = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

// NewStaticLeaseResource is the factory registered with the provider.
func NewStaticLeaseResource() resource.Resource { return &staticLeaseResource{} }

type staticLeaseResource struct{ client *client.Client }

type staticLeaseModel struct {
	MAC types.String `tfsdk:"mac"`
	IP  types.String `tfsdk:"ip"`
}

func (r *staticLeaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_static_lease"
}

func (r *staticLeaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A DHCP static reservation (`ip dhcp host`): the router always hands `ip` to the " +
			"device with `mac`. The reservation is global (one flat list across pools); its IP should fall in a " +
			"segment's DHCP pool subnet. This resource owns only the MAC it declares and never touches others.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Device MAC address, `aa:bb:cc:dd:ee:ff`. Changing it replaces the reservation.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(regexMAC, "must be a MAC like \"aa:bb:cc:dd:ee:ff\"")},
			},
			"ip": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "IPv4 address to reserve for the device.",
			},
		},
	}
}

func (r *staticLeaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *staticLeaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan staticLeaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplyStaticLease(ctx, client.StaticLease{MAC: plan.MAC.ValueString(), IP: plan.IP.ValueString()}); err != nil {
		resp.Diagnostics.AddError("Create static lease failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *staticLeaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state staticLeaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lease, found, err := r.client.GetStaticLease(ctx, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read static lease failed", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	// Keep the MAC as configured (case/format), refresh the IP from the router.
	state.IP = types.StringValue(lease.IP)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *staticLeaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan staticLeaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplyStaticLease(ctx, client.StaticLease{MAC: plan.MAC.ValueString(), IP: plan.IP.ValueString()}); err != nil {
		resp.Diagnostics.AddError("Update static lease failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *staticLeaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state staticLeaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteStaticLease(ctx, state.MAC.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete static lease failed", err.Error())
	}
}

func (r *staticLeaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("mac"), req, resp)
}
