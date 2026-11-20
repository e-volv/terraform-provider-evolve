package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type attrKind int

const (
	kindString attrKind = iota
	kindBool
	kindJSON
)

// attrSpec maps one Terraform attribute to its field in the public launch API
// (`apps/evolve-backend/src/launch/dto/launch.dto.ts`). The wire names are
// camelCase because the API's ValidationPipe whitelists DTO properties and
// silently strips anything else.
type attrSpec struct {
	name string
	wire string // JSON field; "" means not sent
	kind attrKind

	required bool
	computed bool // Optional+Computed: the server supplies a default
	// identity attributes name the URL; changing one is a new resource.
	identity bool
	// readBack false: the API never returns it (targeting lives on the
	// environment, not the flag row), so state keeps what was applied.
	noReadBack bool
	// requiredOnWire: the DTO requires the field, so a null sends this.
	wireDefault any
	// serverOnly: Computed with no Optional; never sent.
	serverOnly bool
}

var idAttr = attrSpec{name: "id", wire: "id", kind: kindString, serverOnly: true}

var kindSpecs = map[string][]attrSpec{
	// CreateFlagProjectDto
	"project": {
		idAttr,
		{name: "key", wire: "key", kind: kindString, required: true, identity: true},
		{name: "name", wire: "name", kind: kindString, required: true},
		{name: "description", wire: "description", kind: kindString},
	},
	// CreateEnvironmentDto
	"environment": {
		idAttr,
		{name: "project_key", kind: kindString, required: true, identity: true},
		{name: "key", wire: "key", kind: kindString, required: true, identity: true},
		{name: "name", wire: "name", kind: kindString, required: true},
		{name: "tone", wire: "tone", kind: kindString, computed: true},
		{name: "critical", wire: "critical", kind: kindBool, computed: true},
		{name: "require_approval", wire: "requireApproval", kind: kindBool, computed: true},
	},
	// PublicLaunchFlagDto (CreateFlagDto + environmentId + targeting)
	"flag": {
		idAttr,
		{name: "project_key", kind: kindString, required: true, identity: true},
		{name: "key", wire: "key", kind: kindString, required: true, identity: true},
		{name: "name", wire: "name", kind: kindString, required: true},
		{name: "description", wire: "description", kind: kindString},
		{name: "type", wire: "type", kind: kindString, required: true},
		{name: "variations_json", wire: "variants", kind: kindJSON, required: true},
		{name: "client_side_available", wire: "clientSideAvailable", kind: kindBool, computed: true},
		{name: "mobile_available", wire: "mobileAvailable", kind: kindBool, computed: true},
		{name: "environment_id", wire: "environmentId", kind: kindString, noReadBack: true},
		{name: "targeting_json", wire: "targeting", kind: kindJSON, noReadBack: true},
		{name: "pending_change_id", kind: kindString, serverOnly: true, noReadBack: true},
	},
	// SegmentDto
	"segment": {
		idAttr,
		{name: "project_key", kind: kindString, required: true, identity: true},
		{name: "key", wire: "key", kind: kindString, required: true, identity: true},
		{name: "name", wire: "name", kind: kindString, required: true},
		{name: "description", wire: "description", kind: kindString},
		{name: "context_kind", wire: "contextKind", kind: kindString, computed: true},
		{name: "mode", wire: "mode", kind: kindString, computed: true},
		{name: "rules_json", wire: "rules", kind: kindJSON, wireDefault: []any{}},
	},
}

// gatedAttrs are applied through the approval path. A 202 leaves them at
// their prior values: the server has not applied them yet.
var gatedAttrs = map[string]bool{"targeting_json": true, "environment_id": true}

// Model is one resource's attribute values, keyed by attribute name.
type Model map[string]attr.Value

func nullOf(spec attrSpec) attr.Value {
	if spec.kind == kindBool {
		return types.BoolNull()
	}
	return types.StringNull()
}

func unknownOf(spec attrSpec) attr.Value {
	if spec.kind == kindBool {
		return types.BoolUnknown()
	}
	return types.StringUnknown()
}

type attrGetter interface {
	GetAttribute(context.Context, path.Path, interface{}) diag.Diagnostics
}

type attrSetter interface {
	SetAttribute(context.Context, path.Path, interface{}) diag.Diagnostics
}

func loadModel(ctx context.Context, specs []attrSpec, from attrGetter) (Model, diag.Diagnostics) {
	var diags diag.Diagnostics
	model := Model{}
	for _, spec := range specs {
		if spec.kind == kindBool {
			var value types.Bool
			diags.Append(from.GetAttribute(ctx, path.Root(spec.name), &value)...)
			model[spec.name] = value
		} else {
			var value types.String
			diags.Append(from.GetAttribute(ctx, path.Root(spec.name), &value)...)
			model[spec.name] = value
		}
	}
	return model, diags
}

func saveModel(ctx context.Context, specs []attrSpec, model Model, to attrSetter) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, spec := range specs {
		value, ok := model[spec.name]
		if !ok || value == nil {
			value = nullOf(spec)
		}
		diags.Append(to.SetAttribute(ctx, path.Root(spec.name), value)...)
	}
	return diags
}

func (m Model) str(name string) string {
	if value, ok := m[name].(types.String); ok && !value.IsNull() && !value.IsUnknown() {
		return value.ValueString()
	}
	return ""
}

// wireBody is the request body for a create or update.
func wireBody(specs []attrSpec, model Model) (map[string]any, error) {
	out := map[string]any{}
	for _, spec := range specs {
		if spec.wire == "" || spec.serverOnly {
			continue
		}
		value := model[spec.name]
		if value == nil || value.IsNull() || value.IsUnknown() {
			if spec.wireDefault != nil {
				out[spec.wire] = spec.wireDefault
			}
			continue
		}
		switch spec.kind {
		case kindBool:
			out[spec.wire] = value.(types.Bool).ValueBool()
		case kindJSON:
			var raw any
			if err := json.Unmarshal([]byte(value.(types.String).ValueString()), &raw); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %w", spec.name, err)
			}
			out[spec.wire] = raw
		default:
			out[spec.wire] = value.(types.String).ValueString()
		}
	}
	return out, nil
}

// mergeServer builds the state from the server's copy of the object. desired
// is the plan (after a write) or the prior state (on read). A known desired
// value that the server agrees with is kept verbatim, so JSON formatting and
// key order never show up as drift.
func mergeServer(specs []attrSpec, desired Model, raw map[string]any) Model {
	out := Model{}
	for _, spec := range specs {
		current := desired[spec.name]
		if current == nil {
			current = nullOf(spec)
		}
		if spec.noReadBack {
			if current.IsUnknown() {
				current = nullOf(spec)
			}
			out[spec.name] = current
			continue
		}
		if spec.wire == "" {
			// Only in the URL (project_key): the server does not echo it.
			out[spec.name] = current
			continue
		}
		serverValue, present := raw[spec.wire]
		out[spec.name] = fromServer(spec, current, serverValue, present)
	}
	return out
}

func fromServer(spec attrSpec, current attr.Value, value any, present bool) attr.Value {
	if !present || value == nil {
		if spec.computed || spec.serverOnly || current.IsUnknown() {
			return nullOf(spec)
		}
		// Optional with nothing on the server: absent and unset agree.
		if !spec.required {
			return nullOf(spec)
		}
		return current
	}
	switch spec.kind {
	case kindBool:
		b, ok := value.(bool)
		if !ok {
			return current
		}
		return types.BoolValue(b)
	case kindJSON:
		encoded, err := json.Marshal(value)
		if err != nil {
			return current
		}
		if s, ok := current.(types.String); ok && !s.IsNull() && !s.IsUnknown() && jsonEqual(s.ValueString(), string(encoded)) {
			return current
		}
		// An empty list on the server for an optional, unset list.
		if !spec.required && !spec.computed && current.IsNull() && jsonEqual(string(encoded), "[]") {
			return current
		}
		return types.StringValue(string(encoded))
	default:
		s := fmt.Sprint(value)
		if s == "" && !spec.required && !spec.computed && current.IsNull() {
			return current
		}
		return types.StringValue(s)
	}
}

// jsonEqual compares two JSON documents by value, not by spelling.
func jsonEqual(a, b string) bool {
	na, errA := normalizeJSON(a)
	nb, errB := normalizeJSON(b)
	return errA == nil && errB == nil && bytes.Equal(na, nb)
}

func normalizeJSON(text string) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// semanticJSON keeps the prior state's spelling when the configuration says
// the same thing differently, so reformatting a jsonencode() is not a diff.
type semanticJSON struct{}

func (semanticJSON) Description(context.Context) string {
	return "Ignores JSON formatting and key-order differences."
}

func (m semanticJSON) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (semanticJSON) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if jsonEqual(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}
