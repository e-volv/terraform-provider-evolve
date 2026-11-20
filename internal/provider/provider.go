package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type EvolveProvider struct{ version string }
type EvolveProviderModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	APIKey   types.String `tfsdk:"api_key"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &EvolveProvider{version: version} }
}

func (p *EvolveProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "evolve"
	resp.Version = p.version
}

func (p *EvolveProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"endpoint": schema.StringAttribute{Optional: true, Description: "e-volv API origin."},
		"api_key":  schema.StringAttribute{Optional: true, Sensitive: true, Description: "Key with launch:manage."},
	}}
}

func (p *EvolveProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config EvolveProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	endpoint := os.Getenv("EVOLVE_API_URL")
	if !config.Endpoint.IsNull() {
		endpoint = config.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = "https://api.e-volv.io"
	}
	key := os.Getenv("EVOLVE_LAUNCH_KEY")
	if !config.APIKey.IsNull() {
		key = config.APIKey.ValueString()
	}
	client := NewClient(endpoint, key)
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *EvolveProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &RESTResource{kind: "project"} },
		func() resource.Resource { return &RESTResource{kind: "environment"} },
		func() resource.Resource { return &RESTResource{kind: "flag"} },
		func() resource.Resource { return &RESTResource{kind: "segment"} },
	}
}

func (p *EvolveProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		func() datasource.DataSource { return &RESTDataSource{kind: "project"} },
		func() datasource.DataSource { return &RESTDataSource{kind: "flag"} },
	}
}
