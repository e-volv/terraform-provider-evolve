package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*RESTResource)(nil)
	_ resource.ResourceWithImportState = (*RESTResource)(nil)
)

type RESTResource struct {
	kind   string
	client *Client
}

func (r *RESTResource) specs() []attrSpec { return kindSpecs[r.kind] }

func (r *RESTResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "evolve_launch_" + r.kind
}

func (r *RESTResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceSchema(r.specs())
}

func resourceSchema(specs []attrSpec) schema.Schema {
	attributes := map[string]schema.Attribute{}
	for _, spec := range specs {
		required := spec.required
		optional := !spec.required && !spec.serverOnly
		computed := spec.computed || spec.serverOnly
		switch spec.kind {
		case kindBool:
			var modifiers []planmodifier.Bool
			if spec.computed {
				modifiers = append(modifiers, boolplanmodifier.UseStateForUnknown())
			}
			attributes[spec.name] = schema.BoolAttribute{Required: required, Optional: optional, Computed: computed, PlanModifiers: modifiers}
		default:
			var modifiers []planmodifier.String
			if spec.identity {
				modifiers = append(modifiers, stringplanmodifier.RequiresReplace())
			}
			// id and the server defaults never change on their own; without
			// this every update would plan them as unknown.
			if spec.computed || spec.name == "id" {
				modifiers = append(modifiers, stringplanmodifier.UseStateForUnknown())
			}
			if spec.kind == kindJSON {
				modifiers = append(modifiers, semanticJSON{})
			}
			attributes[spec.name] = schema.StringAttribute{Required: required, Optional: optional, Computed: computed, PlanModifiers: modifiers}
		}
	}
	return schema.Schema{Attributes: attributes}
}

func (r *RESTResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData != nil {
		r.client = req.ProviderData.(*Client)
	}
}

func (r *RESTResource) collection(project string) string {
	if r.kind == "project" {
		return "/projects"
	}
	return fmt.Sprintf("/projects/%s/%ss", url.PathEscape(project), r.kind)
}

func (r *RESTResource) item(model Model) string {
	if r.kind == "project" {
		return "/projects/" + url.PathEscape(model.str("key"))
	}
	return r.collection(model.str("project_key")) + "/" + url.PathEscape(model.str("key"))
}

// writeResponse is the 202 body PublicLaunchManageController.targeting sends
// when an environment holds the targeting change for approval.
type writeResponse struct {
	Pending  bool `json:"pending"`
	ChangeID any  `json:"changeId"`
}

func (r *RESTResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, diags := loadModel(ctx, r.specs(), req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	prior := Model{}
	state, diags := r.write(ctx, http.MethodPost, r.collection(plan.str("project_key")), plan, prior)
	resp.Diagnostics.Append(diags...)
	if state == nil {
		return
	}
	resp.Diagnostics.Append(saveModel(ctx, r.specs(), state, &resp.State)...)
}

func (r *RESTResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, diags := loadModel(ctx, r.specs(), req.Plan)
	resp.Diagnostics.Append(diags...)
	prior, diags := loadModel(ctx, r.specs(), req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, diags := r.write(ctx, http.MethodPut, r.item(plan), plan, prior)
	resp.Diagnostics.Append(diags...)
	if state == nil {
		return
	}
	resp.Diagnostics.Append(saveModel(ctx, r.specs(), state, &resp.State)...)
}

// write sends the plan, then reads the object back so every computed
// attribute is known in state. A 202 means the targeting change is held for
// approval: everything else was applied, the gated attributes keep their
// prior values, and the change id is recorded.
func (r *RESTResource) write(ctx context.Context, method, target string, plan, prior Model) (Model, diag.Diagnostics) {
	var diags diag.Diagnostics
	body, err := wireBody(r.specs(), plan)
	if err != nil {
		diags.AddError("Invalid configuration", err.Error())
		return nil, diags
	}
	var response writeResponse
	status, err := r.client.Do(ctx, method, target, body, &response)
	if err != nil {
		diags.AddError(fmt.Sprintf("%s %s failed", method, target), err.Error())
		return nil, diags
	}
	var raw map[string]any
	if _, err := r.client.Do(ctx, http.MethodGet, r.item(plan), nil, &raw); err != nil {
		diags.AddError("Read after write failed", err.Error())
		return nil, diags
	}
	state := mergeServer(r.specs(), plan, raw)
	if _, ok := state["pending_change_id"]; ok {
		state["pending_change_id"] = types.StringNull()
	}
	if status == http.StatusAccepted {
		changeID := ""
		if response.ChangeID != nil {
			changeID = fmt.Sprint(response.ChangeID)
		}
		if _, ok := state["pending_change_id"]; ok && changeID != "" {
			state["pending_change_id"] = types.StringValue(changeID)
		}
		if method == http.MethodPost {
			// Terraform requires a created object's state to match its plan
			// unless the create fails, and a failed create is tainted and
			// replaced on the next apply, which would archive the flag. So a
			// held create records the requested targeting, marked pending.
			diags.AddWarning("Targeting awaits approval",
				fmt.Sprintf("The flag was created, but the environment requires approval, so its targeting was not applied. "+
					"Change request %s is pending in e-volv; targeting_json records what was requested, and pending_change_id marks it as not yet applied.", changeID))
			return state, diags
		}
		// An update that is held keeps the previously applied targeting in
		// state. That has to be reported as an error: Terraform rejects a
		// state that differs from the plan after a successful apply. It does
		// not taint an update, and the next plan shows the held change again.
		for name := range gatedAttrs {
			if _, ok := state[name]; !ok {
				continue
			}
			if value, ok := prior[name]; ok && value != nil {
				state[name] = value
			} else {
				state[name] = nullValue(state[name])
			}
		}
		diags.AddError("Targeting change awaits approval",
			fmt.Sprintf("The environment requires approval, so the targeting change was not applied. Change request %s is pending in e-volv. "+
				"Every other attribute was applied. State keeps the previously applied targeting until the change is approved; apply again afterwards.", changeID))
	}
	return state, diags
}

func nullValue(value attr.Value) attr.Value {
	if _, ok := value.(types.Bool); ok {
		return types.BoolNull()
	}
	return types.StringNull()
}

func (r *RESTResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	prior, diags := loadModel(ctx, r.specs(), req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var raw map[string]any
	status, err := r.client.Do(ctx, http.MethodGet, r.item(prior), nil, &raw)
	if status == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read failed", err.Error())
		return
	}
	resp.Diagnostics.Append(saveModel(ctx, r.specs(), mergeServer(r.specs(), prior, raw), &resp.State)...)
}

func (r *RESTResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	prior, diags := loadModel(ctx, r.specs(), req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	status, err := r.client.Do(ctx, http.MethodDelete, r.item(prior), nil, nil)
	if status == http.StatusNotFound {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Delete failed", err.Error())
		return
	}
	if status == http.StatusAccepted {
		// An error, not a warning: only an error keeps the resource in state,
		// and it still exists until someone approves the deletion.
		resp.Diagnostics.AddError("Deletion awaits approval",
			"e-volv accepted the deletion but is holding it for approval. The resource stays in state until the change is approved; run apply again afterwards.")
	}
}

func (r *RESTResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.kind == "project" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), req.ID)...)
		return
	}
	project, key, ok := strings.Cut(req.ID, "/")
	if !ok || project == "" || key == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <project-key>/<resource-key>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_key"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), key)...)
}
