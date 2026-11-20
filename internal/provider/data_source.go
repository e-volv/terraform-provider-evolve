package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

type RESTDataSource struct {
	kind   string
	client *Client
}

// dataSpecs is the resource's attributes minus the ones the API never
// returns: the identity attributes are required inputs, the rest computed.
func dataSpecs(kind string) []attrSpec {
	var out []attrSpec
	for _, spec := range kindSpecs[kind] {
		if spec.noReadBack {
			continue
		}
		out = append(out, spec)
	}
	return out
}

func (d *RESTDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "evolve_launch_" + d.kind
}

func (d *RESTDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := map[string]schema.Attribute{}
	for _, spec := range dataSpecs(d.kind) {
		required := spec.identity
		switch spec.kind {
		case kindBool:
			attributes[spec.name] = schema.BoolAttribute{Required: required, Computed: !required}
		default:
			attributes[spec.name] = schema.StringAttribute{Required: required, Computed: !required}
		}
	}
	resp.Schema = schema.Schema{Attributes: attributes}
}

func (d *RESTDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	if req.ProviderData != nil {
		d.client = req.ProviderData.(*Client)
	}
}

func (d *RESTDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	specs := dataSpecs(d.kind)
	model, diags := loadModel(ctx, specs, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resource := &RESTResource{kind: d.kind, client: d.client}
	var raw map[string]any
	if _, err := d.client.Do(ctx, http.MethodGet, resource.item(model), nil, &raw); err != nil {
		resp.Diagnostics.AddError("Read failed", err.Error())
		return
	}
	resp.Diagnostics.Append(saveModel(ctx, specs, mergeServer(specs, model, raw), &resp.State)...)
}
