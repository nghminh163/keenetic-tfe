package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nghminh163/terraform-provider-keenetic/internal/client"
)

var (
	_ resource.Resource              = (*mdnsReflectorResource)(nil)
	_ resource.ResourceWithConfigure = (*mdnsReflectorResource)(nil)
)

// NewMDNSReflectorResource is the factory registered with the provider.
func NewMDNSReflectorResource() resource.Resource { return &mdnsReflectorResource{} }

type mdnsReflectorResource struct{ client *client.Client }

type mdnsReflectorModel struct {
	Enforce types.Bool `tfsdk:"enforce"`
}

func (r *mdnsReflectorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mdns_reflector"
}

func (r *mdnsReflectorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The router-wide mDNS reflector, which relays multicast DNS (Bonjour/AirPlay, " +
			"Chromecast discovery, …) between the router's segments. This is a single global toggle, not a " +
			"per-segment setting — manage at most one of these. Deleting the resource leaves the router's current " +
			"setting untouched.",
		Attributes: map[string]schema.Attribute{
			"enforce": schema.BoolAttribute{
				Required:            true,
				MarkdownDescription: "Whether the mDNS reflector is enforced (on) across segments.",
			},
		},
	}
}

func (r *mdnsReflectorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *mdnsReflectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mdnsReflectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetMDNSReflector(ctx, plan.Enforce.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Set mDNS reflector failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *mdnsReflectorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mdnsReflectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	enforce, err := r.client.GetMDNSReflector(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Read mDNS reflector failed", err.Error())
		return
	}
	state.Enforce = types.BoolValue(enforce)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *mdnsReflectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mdnsReflectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetMDNSReflector(ctx, plan.Enforce.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Set mDNS reflector failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *mdnsReflectorResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Global, router-wide setting: dropping the resource from state must not flip mDNS for the whole
	// router. Leave the current value in place.
	resp.Diagnostics.AddWarning(
		"mDNS reflector left unchanged",
		"keenetic_mdns_reflector is a global toggle; removing it from Terraform leaves the router's current mDNS setting as-is. Change `enforce` and apply if you want to turn it off.",
	)
}
