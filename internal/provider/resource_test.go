package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// fakeAPI is a minimal PublicLaunchManageController.
type fakeAPI struct {
	mu       sync.Mutex
	bodies   []map[string]any
	flag     map[string]any
	status   int // status for writes; 0 means 201/200
	missing  bool
	deleteSt int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if f.missing {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"statusCode":404,"message":"Launch flag not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(f.flag)
	case http.MethodPost, http.MethodPut:
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies = append(f.bodies, body)
		if f.status == 202 {
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"pending":true,"changeId":"flag_scheduled_change:abc","flag":{}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(f.flag)
	case http.MethodDelete:
		if f.deleteSt != 0 {
			w.WriteHeader(f.deleteSt)
			return
		}
		w.WriteHeader(204)
	}
}

func flagServerRow() map[string]any {
	return map[string]any{
		"id": "flag:f1", "key": "checkout.new", "name": "New checkout", "type": "boolean",
		"variants":            []any{map[string]any{"value": false, "key": "off"}, map[string]any{"key": "on", "value": true}},
		"clientSideAvailable": false, "workspace": "workspace:w", "project": "flag_project:p",
	}
}

func flagResource(t *testing.T, api *fakeAPI) (*RESTResource, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return &RESTResource{kind: "flag", client: NewClient(server.URL, "evk_test")}, server
}

func schemaOf(r *RESTResource) tfsdk.State {
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return tfsdk.State{Schema: resp.Schema}
}

// objectValue builds a raw value for the schema, unknown for every computed
// attribute not given (as Terraform plans them).
func objectValue(t *testing.T, r *RESTResource, values map[string]tftypes.Value, unknownComputed bool) tftypes.Value {
	t.Helper()
	state := schemaOf(r)
	objectType := state.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	out := map[string]tftypes.Value{}
	for _, spec := range r.specs() {
		typ := objectType.AttributeTypes[spec.name]
		if value, ok := values[spec.name]; ok {
			out[spec.name] = value
		} else if unknownComputed && (spec.computed || spec.serverOnly) {
			out[spec.name] = tftypes.NewValue(typ, tftypes.UnknownValue)
		} else {
			out[spec.name] = tftypes.NewValue(typ, nil)
		}
	}
	return tftypes.NewValue(objectType, out)
}

func str(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }

func plannedFlag(t *testing.T, r *RESTResource, extra map[string]tftypes.Value) tfsdk.Plan {
	values := map[string]tftypes.Value{
		"project_key":     str("shop"),
		"key":             str("checkout.new"),
		"name":            str("New checkout"),
		"type":            str("boolean"),
		"environment_id":  str("flag_environment:prod"),
		"variations_json": str(`[ {"key": "off", "value": false}, {"key": "on", "value": true} ]`),
	}
	for k, v := range extra {
		values[k] = v
	}
	return tfsdk.Plan{Schema: schemaOf(r).Schema, Raw: objectValue(t, r, values, true)}
}

func TestCreateReadsBackComputedAttributes(t *testing.T) {
	api := &fakeAPI{flag: flagServerRow()}
	r, _ := flagResource(t, api)
	ctx := context.Background()
	plan := plannedFlag(t, r, nil)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema, Raw: objectValue(t, r, nil, false)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if !resp.State.Raw.IsFullyKnown() {
		t.Fatalf("state has unknown values: %s", resp.State.Raw)
	}
	var id, variants types.String
	var clientSide types.Bool
	resp.State.GetAttribute(ctx, pathOf("id"), &id)
	resp.State.GetAttribute(ctx, pathOf("variations_json"), &variants)
	resp.State.GetAttribute(ctx, pathOf("client_side_available"), &clientSide)
	if id.ValueString() != "flag:f1" || clientSide.IsNull() || clientSide.ValueBool() {
		t.Fatalf("id=%s clientSide=%s", id, clientSide)
	}
	// The planned spelling is kept: the server's differs only in format.
	if !strings.HasPrefix(variants.ValueString(), "[ {") {
		t.Fatalf("variations_json=%s", variants)
	}
	body := api.bodies[0]
	if body["environmentId"] != "flag_environment:prod" {
		t.Fatalf("body=%v", body)
	}
	for _, forbidden := range []string{"environment_id", "project_key", "variations_json", "id", "pending_change_id"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("body carries %s: %v", forbidden, body)
		}
	}
}

func TestUpdateHeldForApprovalKeepsPriorTargeting(t *testing.T) {
	api := &fakeAPI{flag: flagServerRow(), status: 202}
	r, _ := flagResource(t, api)
	ctx := context.Background()
	prior := tfsdk.State{Schema: schemaOf(r).Schema, Raw: objectValue(t, r, map[string]tftypes.Value{
		"id": str("flag:f1"), "project_key": str("shop"), "key": str("checkout.new"), "name": str("New checkout"),
		"type": str("boolean"), "variations_json": str(`[{"key":"off","value":false},{"key":"on","value":true}]`),
		"environment_id": str("flag_environment:prod"), "targeting_json": str(`{"defaultVariant":"off","rules":[]}`),
		"client_side_available": tftypes.NewValue(tftypes.Bool, false),
	}, false)}
	plan := plannedFlag(t, r, map[string]tftypes.Value{
		"id":             str("flag:f1"),
		"targeting_json": str(`{"defaultVariant":"on","rules":[]}`),
	})
	resp := resource.UpdateResponse{State: prior}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: prior}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a held update must be reported")
	}
	var targeting, pending types.String
	resp.State.GetAttribute(ctx, pathOf("targeting_json"), &targeting)
	resp.State.GetAttribute(ctx, pathOf("pending_change_id"), &pending)
	if targeting.ValueString() != `{"defaultVariant":"off","rules":[]}` {
		t.Fatalf("targeting saved as applied: %s", targeting)
	}
	if pending.ValueString() != "flag_scheduled_change:abc" {
		t.Fatalf("pending_change_id=%s", pending)
	}
	if _, ok := api.bodies[0]["targeting"]; !ok {
		t.Fatalf("targeting not sent: %v", api.bodies[0])
	}
}

func TestCreateHeldForApprovalWarnsAndRecordsChangeID(t *testing.T) {
	api := &fakeAPI{flag: flagServerRow(), status: 202}
	r, _ := flagResource(t, api)
	ctx := context.Background()
	plan := plannedFlag(t, r, map[string]tftypes.Value{"targeting_json": str(`{"defaultVariant":"on","rules":[]}`)})
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema, Raw: objectValue(t, r, nil, false)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("diagnostics=%v", resp.Diagnostics)
	}
	var pending types.String
	resp.State.GetAttribute(ctx, pathOf("pending_change_id"), &pending)
	if pending.ValueString() != "flag_scheduled_change:abc" {
		t.Fatalf("pending_change_id=%s", pending)
	}
}

func TestReadRemovesMissingResource(t *testing.T) {
	api := &fakeAPI{missing: true}
	r, _ := flagResource(t, api)
	state := tfsdk.State{Schema: schemaOf(r).Schema, Raw: objectValue(t, r, map[string]tftypes.Value{
		"project_key": str("shop"), "key": str("checkout.new"),
	}, false)}
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Fatalf("diags=%v state=%s", resp.Diagnostics, resp.State.Raw)
	}
}

func TestReadIgnoresJSONFormatting(t *testing.T) {
	api := &fakeAPI{flag: flagServerRow()}
	r, _ := flagResource(t, api)
	spelled := "[\n  {\"key\": \"off\", \"value\": false},\n  {\"value\": true, \"key\": \"on\"}\n]"
	state := tfsdk.State{Schema: schemaOf(r).Schema, Raw: objectValue(t, r, map[string]tftypes.Value{
		"project_key": str("shop"), "key": str("checkout.new"), "variations_json": str(spelled),
	}, false)}
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	var variants types.String
	resp.State.GetAttribute(context.Background(), pathOf("variations_json"), &variants)
	if variants.ValueString() != spelled {
		t.Fatalf("variations_json=%s", variants)
	}
}

func TestDeleteHeldForApprovalKeepsState(t *testing.T) {
	api := &fakeAPI{deleteSt: 202}
	r, _ := flagResource(t, api)
	state := tfsdk.State{Schema: schemaOf(r).Schema, Raw: objectValue(t, r, map[string]tftypes.Value{
		"project_key": str("shop"), "key": str("checkout.new"),
	}, false)}
	resp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a held delete must keep the resource in state")
	}
}

func TestWireBodiesMatchDTOs(t *testing.T) {
	model := Model{
		"project_key": types.StringValue("shop"), "key": types.StringValue("production"), "name": types.StringValue("Production"),
		"critical": types.BoolValue(true), "require_approval": types.BoolValue(true), "tone": types.StringUnknown(),
		"id": types.StringUnknown(),
	}
	body, err := wireBody(kindSpecs["environment"], model)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"key": "production", "name": "Production", "critical": true, "requireApproval": true}
	if len(body) != len(want) {
		t.Fatalf("body=%v", body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Fatalf("body[%s]=%v", k, body[k])
		}
	}
	segment, _ := wireBody(kindSpecs["segment"], Model{"key": types.StringValue("beta"), "name": types.StringValue("Beta"), "context_kind": types.StringValue("org")})
	if _, ok := segment["rules"]; !ok || segment["contextKind"] != "org" {
		t.Fatalf("segment body=%v", segment)
	}
}

func TestJSONEqual(t *testing.T) {
	if !jsonEqual(`{"a":1,"b":[1,2]}`, "{ \"b\": [1, 2], \"a\": 1 }") {
		t.Fatal("same document reported different")
	}
	if jsonEqual(`{"a":1}`, `{"a":2}`) || jsonEqual(`[1,2]`, `[2,1]`) {
		t.Fatal("different documents reported equal")
	}
}

func pathOf(name string) path.Path { return path.Root(name) }

func TestSchemasAreValid(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	for _, factory := range p.Resources(ctx) {
		var resp resource.SchemaResponse
		factory().Schema(ctx, resource.SchemaRequest{}, &resp)
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatal(diags)
		}
	}
}
