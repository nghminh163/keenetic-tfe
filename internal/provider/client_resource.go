package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                = (*clientResource)(nil)
	_ resource.ResourceWithConfigure   = (*clientResource)(nil)
	_ resource.ResourceWithImportState = (*clientResource)(nil)
)

// NewClientResource is the factory registered with the provider.
func NewClientResource() resource.Resource { return &clientResource{} }

type clientResource struct{ client *client.Client }

type clientModel struct {
	Name types.String `tfsdk:"name"`
	MAC  types.String `tfsdk:"mac"`
	IP   types.String `tfsdk:"ip"`
}

func (r *clientResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client"
}

func (r *clientResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A registered device (\"client\"): a named host (`known host <name> <mac>`, the name " +
			"shown in the router's device list) with an optional reserved IP (`ip dhcp host`). Identified by MAC; " +
			"owns only the MAC it declares.",
		Attributes: map[string]schema.Attribute{
			"mac": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Device MAC, `aa:bb:cc:dd:ee:ff`. Changing it replaces the client.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(regexMAC, "must be a MAC like \"aa:bb:cc:dd:ee:ff\"")},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Device name shown in the router UI (the `known host` label).",
			},
			"ip": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Static IPv4 reservation for this device. Omit for no reservation.",
			},
		},
	}
}

func (r *clientResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *clientResource) toClient(m clientModel) client.KnownClient {
	return client.KnownClient{Name: m.Name.ValueString(), MAC: m.MAC.ValueString(), IP: m.IP.ValueString()}
}

func (r *clientResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clientModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplyClient(ctx, r.toClient(plan)); err != nil {
		resp.Diagnostics.AddError("Create client failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *clientResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clientModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kc, found, err := r.client.GetClient(ctx, state.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read client failed", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Name = types.StringValue(kc.Name)
	state.IP = stringOrNull(kc.IP)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *clientResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan clientModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplyClient(ctx, r.toClient(plan)); err != nil {
		resp.Diagnostics.AddError("Update client failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *clientResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clientModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteClient(ctx, state.MAC.ValueString()); err != nil {
		resp.Diagnostics.AddError("Delete client failed", err.Error())
	}
}

func (r *clientResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("mac"), req, resp)
}
