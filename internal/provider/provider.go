package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nghminh163/terraform-provider-keenetic/internal/client"
)

// Ensure the provider satisfies the framework interface.
var _ provider.Provider = (*keeneticProvider)(nil)

type keeneticProvider struct {
	version string
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &keeneticProvider{version: version}
	}
}

func (p *keeneticProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "keenetic"
	resp.Version = p.version
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (p *keeneticProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a Keenetic router through its RCI HTTP API.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Router web base URL, e.g. `http://192.168.18.1`. Falls back to `KEENETIC_ENDPOINT`.",
			},
			"username": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Admin username. Falls back to `KEENETIC_USERNAME`.",
			},
			"password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Admin password. Falls back to `KEENETIC_PASSWORD`.",
			},
		},
	}
}

func (p *keeneticProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := valueOrEnv(cfg.Endpoint, "KEENETIC_ENDPOINT")
	username := valueOrEnv(cfg.Username, "KEENETIC_USERNAME")
	password := valueOrEnv(cfg.Password, "KEENETIC_PASSWORD")

	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("endpoint"), "Missing endpoint", "Set the provider `endpoint` or the KEENETIC_ENDPOINT environment variable.")
	}
	if username == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("username"), "Missing username", "Set the provider `username` or the KEENETIC_USERNAME environment variable.")
	}
	if password == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("password"), "Missing password", "Set the provider `password` or the KEENETIC_PASSWORD environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(endpoint, username, password)
	if err != nil {
		resp.Diagnostics.AddError("Cannot build Keenetic client", err.Error())
		return
	}
	resp.ResourceData = c
}

func (p *keeneticProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSegmentResource,
		NewStaticLeaseResource,
		NewMDNSReflectorResource,
		NewClientResource,
	}
}

func (p *keeneticProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func pathRoot(name string) path.Path {
	return path.Root(name)
}

func valueOrEnv(v types.String, env string) string {
	if !v.IsNull() && v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}
