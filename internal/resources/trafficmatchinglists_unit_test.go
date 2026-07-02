package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// --- harness -----------------------------------------------------------------

func tmlSchema(t *testing.T) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	(&trafficMatchingListResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("schema: %v", sr.Diagnostics)
	}
	return tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
}

func tmlResource(t *testing.T, tm *official.TrafficMatchingListsClientMock, opts ...testutil.Opt) *trafficMatchingListResource {
	t.Helper()
	r, ok := NewTrafficMatchingListResource().(*trafficMatchingListResource)
	if !ok {
		t.Fatal("NewTrafficMatchingListResource returned the wrong type")
	}
	oc := &official.ClientMock{TrafficMatchingListsFunc: func() official.TrafficMatchingListsClient { return tm }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure: %v", resp.Diagnostics)
	}
	return r
}

func tmlBuildPlan(t *testing.T, m trafficMatchingListModel) tfsdk.Plan {
	t.Helper()
	empty := tmlSchema(t)
	p := tfsdk.Plan(empty)
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building plan: %v", d)
	}
	return p
}

func tmlBuildState(t *testing.T, m trafficMatchingListModel) tfsdk.State {
	t.Helper()
	s := tmlSchema(t)
	if d := s.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building state: %v", d)
	}
	return s
}

func tmlWantErr(t *testing.T, diags diag.Diagnostics, want string) {
	t.Helper()
	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic containing %q, got none", want)
	}
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), want) {
			return
		}
	}
	t.Fatalf("no error summary contains %q, got %v", want, diags)
}

// tmlFromJSON builds a TrafficMatchingList the same way the API client does:
// by unmarshaling the wire shape, which populates the internal union.
func tmlFromJSON(t *testing.T, raw string) *official.TrafficMatchingList {
	t.Helper()
	var v official.TrafficMatchingList
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &v
}

// tmlStateModel is a minimal valid state for Read/Delete driving.
func tmlStateModel(id string) trafficMatchingListModel {
	return trafficMatchingListModel{
		ID:   types.StringValue(id),
		Name: types.StringValue("lan-list"),
		Type: types.StringValue("IPV4_ADDRESSES"),
	}
}

func tmlPortPlanModel(id types.String, value string) trafficMatchingListModel {
	return trafficMatchingListModel{
		ID:   id,
		Name: types.StringValue("svc-ports"),
		Type: types.StringValue("PORTS"),
		Items: []trafficMatchingListItemModel{{
			MatchType: types.StringValue("PORT_NUMBER"),
			Value:     types.StringValue(value),
			Start:     types.StringNull(),
			Stop:      types.StringNull(),
		}},
	}
}

// --- Metadata / Schema / Configure --------------------------------------------

func TestTrafficMatchingListMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	(&trafficMatchingListResource{}).Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_traffic_matching_list" {
		t.Errorf("type name = %q, want unifi_traffic_matching_list", resp.TypeName)
	}
}

func TestTrafficMatchingListSchemaAttributes(t *testing.T) {
	s := tmlSchema(t)
	for _, name := range []string{"id", "name", "type", "items"} {
		if _, ok := s.Schema.GetAttributes()[name]; !ok {
			t.Errorf("schema missing attribute %q", name)
		}
	}
}

func TestTrafficMatchingListConfigure(t *testing.T) {
	r := &trafficMatchingListResource{}

	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() || r.data != nil {
		t.Errorf("nil provider data should be a no-op, diags %v", resp.Diagnostics)
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 3.14}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Unexpected provider data")

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(&official.ClientMock{})}, &resp)
	if resp.Diagnostics.HasError() || r.data == nil {
		t.Errorf("configure with provider data failed: %v", resp.Diagnostics)
	}
}

// --- Create --------------------------------------------------------------------

func TestTrafficMatchingListCreateReadOnly(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{}, testutil.ReadOnly())
	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &resp)
	tmlWantErr(t, resp.Diagnostics, "read-only")
}

func TestTrafficMatchingListCreateBadPlan(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	empty := tmlSchema(t)
	req := resource.CreateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "junk")}}
	resp := resource.CreateResponse{State: tmlSchema(t)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func TestTrafficMatchingListCreateExpandError(t *testing.T) {
	// The unstubbed Create mock would panic if the expand error did not stop
	// the flow before the API call.
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringUnknown(), "https"))
	resp := resource.CreateResponse{State: tmlSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Invalid port value")
}

func TestTrafficMatchingListCreateAPIError(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		CreateFunc: func(_ context.Context, site uuid.UUID, body official.TrafficMatchingListCreateOrUpdate) (*official.TrafficMatchingList, error) {
			if site != testutil.SiteID {
				t.Errorf("site = %s, want %s", site, testutil.SiteID)
			}
			if body.Name != "svc-ports" || body.Type != "PORTS" {
				t.Errorf("body name/type = %q/%q, want svc-ports/PORTS", body.Name, body.Type)
			}
			return nil, errors.New("boom")
		},
	}
	r := tmlResource(t, tm)
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringUnknown(), "443"))
	resp := resource.CreateResponse{State: tmlSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Failed to create traffic matching list")
}

func TestTrafficMatchingListCreateOK(t *testing.T) {
	listID := uuid.New()
	tm := &official.TrafficMatchingListsClientMock{
		CreateFunc: func(context.Context, uuid.UUID, official.TrafficMatchingListCreateOrUpdate) (*official.TrafficMatchingList, error) {
			return &official.TrafficMatchingList{Id: listID}, nil
		},
	}
	r := tmlResource(t, tm)
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringUnknown(), "443"))
	resp := resource.CreateResponse{State: tmlSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	var got trafficMatchingListModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != listID.String() {
		t.Errorf("id = %q, want %q", got.ID.ValueString(), listID)
	}
	if got.Name.ValueString() != "svc-ports" || len(got.Items) != 1 {
		t.Errorf("state = %+v, want svc-ports with 1 item", got)
	}
}

// --- Read ----------------------------------------------------------------------

func TestTrafficMatchingListReadBadState(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	empty := tmlSchema(t)
	req := resource.ReadRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Bool, true)}}
	resp := resource.ReadResponse{State: tmlSchema(t)}
	r.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestTrafficMatchingListReadBadID(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	st := tmlBuildState(t, tmlStateModel("not-a-uuid"))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Invalid traffic matching list id")
}

func TestTrafficMatchingListReadNotFoundRemoves(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.TrafficMatchingList, error) {
			return nil, fmt.Errorf("list: %w", unifi.ErrNotFound)
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(uuid.New().String()))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found read should not error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state should be removed when the list is gone")
	}
}

func TestTrafficMatchingListReadAPIError(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.TrafficMatchingList, error) {
			return nil, errors.New("boom")
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(uuid.New().String()))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Failed to read traffic matching list")
}

func TestTrafficMatchingListReadFlattenError(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.TrafficMatchingList, error) {
			// Empty union: the type discriminator says IPv4 but there is no
			// union payload to decode.
			return &official.TrafficMatchingList{Id: uuid.New(), Name: "lan-list", Type: "IPV4_ADDRESSES"}, nil
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(uuid.New().String()))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Failed to decode IPv4 matching list")
}

func TestTrafficMatchingListReadOK(t *testing.T) {
	listID := uuid.New()
	tm := &official.TrafficMatchingListsClientMock{
		GetFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) (*official.TrafficMatchingList, error) {
			if site != testutil.SiteID || id != listID {
				t.Errorf("get called with site %s id %s", site, id)
			}
			return tmlFromJSON(t, `{
				"id": "`+listID.String()+`",
				"name": "lan-list",
				"type": "IPV4_ADDRESSES",
				"items": [
					{"type": "IP_ADDRESS", "value": "192.0.2.10"},
					{"type": "SUBNET", "value": "198.51.100.0/24"},
					{"type": "IP_ADDRESS_RANGE", "start": "192.0.2.1", "stop": "192.0.2.9"}
				]
			}`), nil
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(listID.String()))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got trafficMatchingListModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items = %+v, want 3", got.Items)
	}
	if got.Items[0].Value.ValueString() != "192.0.2.10" {
		t.Errorf("item[0].value = %q, want 192.0.2.10", got.Items[0].Value.ValueString())
	}
	if got.Items[2].Start.ValueString() != "192.0.2.1" || got.Items[2].Stop.ValueString() != "192.0.2.9" {
		t.Errorf("item[2] range = %q-%q, want 192.0.2.1-192.0.2.9",
			got.Items[2].Start.ValueString(), got.Items[2].Stop.ValueString())
	}
}

// --- Update --------------------------------------------------------------------

func TestTrafficMatchingListUpdateReadOnly(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{}, testutil.ReadOnly())
	var resp resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{}, &resp)
	tmlWantErr(t, resp.Diagnostics, "read-only")
}

func TestTrafficMatchingListUpdateBadPlan(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	empty := tmlSchema(t)
	req := resource.UpdateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Number, 1)}}
	resp := resource.UpdateResponse{State: tmlSchema(t)}
	r.Update(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func TestTrafficMatchingListUpdateBadID(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringValue("not-a-uuid"), "443"))
	resp := resource.UpdateResponse{State: tmlSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Invalid traffic matching list id")
}

func TestTrafficMatchingListUpdateExpandError(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringValue(uuid.New().String()), "https"))
	resp := resource.UpdateResponse{State: tmlSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Invalid port value")
}

func TestTrafficMatchingListUpdateAPIError(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		UpdateFunc: func(context.Context, uuid.UUID, uuid.UUID, official.TrafficMatchingListCreateOrUpdate) (*official.TrafficMatchingList, error) {
			return nil, errors.New("boom")
		},
	}
	r := tmlResource(t, tm)
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringValue(uuid.New().String()), "443"))
	resp := resource.UpdateResponse{State: tmlSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Failed to update traffic matching list")
}

func TestTrafficMatchingListUpdateOK(t *testing.T) {
	listID := uuid.New()
	tm := &official.TrafficMatchingListsClientMock{
		UpdateFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID, body official.TrafficMatchingListCreateOrUpdate) (*official.TrafficMatchingList, error) {
			if site != testutil.SiteID || id != listID {
				t.Errorf("update called with site %s id %s", site, id)
			}
			if body.Name != "svc-ports" {
				t.Errorf("body name = %q, want svc-ports", body.Name)
			}
			return &official.TrafficMatchingList{Id: listID}, nil
		},
	}
	r := tmlResource(t, tm)
	plan := tmlBuildPlan(t, tmlPortPlanModel(types.StringValue(listID.String()), "8443"))
	resp := resource.UpdateResponse{State: tmlSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	var got trafficMatchingListModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != listID.String() || got.Items[0].Value.ValueString() != "8443" {
		t.Errorf("state = %+v, want id %s item 8443", got, listID)
	}
}

// --- Delete --------------------------------------------------------------------

func TestTrafficMatchingListDeleteGuards(t *testing.T) {
	for name, opt := range map[string]testutil.Opt{
		"read_only":          testutil.ReadOnly(),
		"destroy_protection": testutil.DestroyProtection(),
	} {
		t.Run(name, func(t *testing.T) {
			r := tmlResource(t, &official.TrafficMatchingListsClientMock{}, opt)
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{}, &resp)
			tmlWantErr(t, resp.Diagnostics, "Delete blocked")
		})
	}
}

func TestTrafficMatchingListDeleteBadState(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	empty := tmlSchema(t)
	req := resource.DeleteRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "junk")}}
	var resp resource.DeleteResponse
	r.Delete(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestTrafficMatchingListDeleteBadID(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	st := tmlBuildState(t, tmlStateModel("not-a-uuid"))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Invalid traffic matching list id")
}

func TestTrafficMatchingListDeleteAPIError(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New("boom")
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(uuid.New().String()))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	tmlWantErr(t, resp.Diagnostics, "Failed to delete traffic matching list")
}

func TestTrafficMatchingListDeleteNotFoundIsSuccess(t *testing.T) {
	tm := &official.TrafficMatchingListsClientMock{
		DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return fmt.Errorf("delete: %w", unifi.ErrNotFound)
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(uuid.New().String()))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found delete should succeed: %v", resp.Diagnostics)
	}
}

func TestTrafficMatchingListDeleteOK(t *testing.T) {
	listID := uuid.New()
	tm := &official.TrafficMatchingListsClientMock{
		DeleteFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) error {
			if site != testutil.SiteID || id != listID {
				t.Errorf("delete called with site %s id %s", site, id)
			}
			return nil
		},
	}
	r := tmlResource(t, tm)
	st := tmlBuildState(t, tmlStateModel(listID.String()))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
}

// --- ImportState -----------------------------------------------------------------

func TestTrafficMatchingListImportState(t *testing.T) {
	r := tmlResource(t, &official.TrafficMatchingListsClientMock{})
	id := uuid.New().String()
	resp := resource.ImportStateResponse{State: tmlSchema(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	var got trafficMatchingListModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != id {
		t.Errorf("imported id = %q, want %q", got.ID.ValueString(), id)
	}
}

// --- expand helpers ---------------------------------------------------------------

// TestExpandTrafficMatchingListIPv6 covers the IPv6 union variant end to end.
func TestExpandTrafficMatchingListIPv6(t *testing.T) {
	model := trafficMatchingListModel{
		Name: types.StringValue("v6-list"),
		Type: types.StringValue("IPV6_ADDRESSES"),
		Items: []trafficMatchingListItemModel{
			{MatchType: types.StringValue("IP_ADDRESS"), Value: types.StringValue("2001:db8::10")},
			{MatchType: types.StringValue("SUBNET"), Value: types.StringValue("2001:db8::/64")},
		},
	}
	got := marshalTML(t, model)
	if got["type"] != "IPV6_ADDRESSES" || got["name"] != "v6-list" {
		t.Errorf("outer fields = %v/%v, want IPV6_ADDRESSES/v6-list", got["type"], got["name"])
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 entries", got["items"])
	}
	addr := items[0].(map[string]any)
	if addr["type"] != "IP_ADDRESS" || addr["value"] != "2001:db8::10" {
		t.Errorf("item[0] = %v, want IP_ADDRESS 2001:db8::10", addr)
	}
	subnet := items[1].(map[string]any)
	if subnet["type"] != "SUBNET" || subnet["value"] != "2001:db8::/64" {
		t.Errorf("item[1] = %v, want SUBNET 2001:db8::/64", subnet)
	}
}

func TestExpandTrafficMatchingListUnsupportedType(t *testing.T) {
	_, diags := expandTrafficMatchingList(trafficMatchingListModel{
		Name: types.StringValue("x"),
		Type: types.StringValue("MAC_ADDRESSES"),
	})
	tmlWantErr(t, diags, "Unsupported list type")
}

func TestExpandItemsInvalidMatchTypes(t *testing.T) {
	bad := []trafficMatchingListItemModel{{MatchType: types.StringValue("BOGUS")}}

	var d4 diag.Diagnostics
	if out := expandIPv4Items(bad, &d4); len(out) != 0 {
		t.Errorf("ipv4 out = %v, want empty", out)
	}
	tmlWantErr(t, d4, "Invalid IPv4 match_type")

	var d6 diag.Diagnostics
	if out := expandIPv6Items(bad, &d6); len(out) != 0 {
		t.Errorf("ipv6 out = %v, want empty", out)
	}
	tmlWantErr(t, d6, "Invalid IPv6 match_type")

	var dp diag.Diagnostics
	if out := expandPortItems(bad, &dp); len(out) != 0 {
		t.Errorf("ports out = %v, want empty", out)
	}
	tmlWantErr(t, dp, "Invalid port match_type")
}

// TestExpandPortItemsSkipsBadPorts covers the parse-failure continues for both
// port variants and the out-of-range parsePort branch.
func TestExpandPortItemsSkipsBadPorts(t *testing.T) {
	var diags diag.Diagnostics
	out := expandPortItems([]trafficMatchingListItemModel{
		{MatchType: types.StringValue("PORT_NUMBER"), Value: types.StringValue("not-a-port")},
		{MatchType: types.StringValue("PORT_NUMBER_RANGE"), Start: types.StringValue("70000"), Stop: types.StringValue("80")},
	}, &diags)
	if len(out) != 0 {
		t.Errorf("out = %v, want empty (both items skipped)", out)
	}
	tmlWantErr(t, diags, "Invalid port value")
	tmlWantErr(t, diags, "Port out of range")
}

// --- flatten helpers ---------------------------------------------------------------

func TestFlattenTrafficMatchingListIPv6(t *testing.T) {
	m, diags := flattenTrafficMatchingList(tmlFromJSON(t, `{
		"id": "`+uuid.New().String()+`",
		"name": "v6-list",
		"type": "IPV6_ADDRESSES",
		"items": [
			{"type": "IP_ADDRESS", "value": "2001:db8::10"},
			{"type": "SUBNET", "value": "2001:db8::/64"}
		]
	}`))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if len(m.Items) != 2 || m.Items[0].Value.ValueString() != "2001:db8::10" ||
		m.Items[1].Value.ValueString() != "2001:db8::/64" {
		t.Errorf("items = %+v", m.Items)
	}
}

func TestFlattenTrafficMatchingListPorts(t *testing.T) {
	m, diags := flattenTrafficMatchingList(tmlFromJSON(t, `{
		"id": "`+uuid.New().String()+`",
		"name": "svc-ports",
		"type": "PORTS",
		"items": [
			{"type": "PORT_NUMBER", "value": 443},
			{"type": "PORT_NUMBER_RANGE", "start": 8000, "stop": 8100},
			{"type": "PORT_NUMBER"}
		]
	}`))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if len(m.Items) != 3 {
		t.Fatalf("items = %+v, want 3", m.Items)
	}
	if m.Items[0].Value.ValueString() != "443" {
		t.Errorf("item[0].value = %q, want 443", m.Items[0].Value.ValueString())
	}
	if m.Items[1].Start.ValueString() != "8000" || m.Items[1].Stop.ValueString() != "8100" {
		t.Errorf("item[1] = %+v, want 8000-8100", m.Items[1])
	}
	// A number-less port matcher surfaces as a null value, not "0".
	if !m.Items[2].Value.IsNull() {
		t.Errorf("item[2].value = %v, want null", m.Items[2].Value)
	}
}

// TestFlattenTrafficMatchingListValueNull covers the null-value IPv4 item path
// (optString on a nil pointer).
func TestFlattenTrafficMatchingListValueNull(t *testing.T) {
	m, diags := flattenTrafficMatchingList(tmlFromJSON(t, `{
		"id": "`+uuid.New().String()+`",
		"name": "lan-list",
		"type": "IPV4_ADDRESSES",
		"items": [{"type": "IP_ADDRESS"}]
	}`))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if len(m.Items) != 1 || !m.Items[0].Value.IsNull() {
		t.Errorf("items = %+v, want one null-value item", m.Items)
	}
}

func TestFlattenTrafficMatchingListDecodeErrors(t *testing.T) {
	for listType, want := range map[string]string{
		"IPV4_ADDRESSES": "Failed to decode IPv4 matching list",
		"IPV6_ADDRESSES": "Failed to decode IPv6 matching list",
		"PORTS":          "Failed to decode port matching list",
	} {
		t.Run(listType, func(t *testing.T) {
			// No union payload behind the discriminator: decoding must fail.
			_, diags := flattenTrafficMatchingList(&official.TrafficMatchingList{
				Id: uuid.New(), Name: "broken", Type: listType,
			})
			tmlWantErr(t, diags, want)
		})
	}
}

func TestFlattenTrafficMatchingListUnsupportedType(t *testing.T) {
	_, diags := flattenTrafficMatchingList(&official.TrafficMatchingList{
		Id: uuid.New(), Name: "odd", Type: "BOGUS",
	})
	tmlWantErr(t, diags, "Unsupported list type from API")
}

func TestFlattenItemsNilAndBrokenDiscriminators(t *testing.T) {
	t.Run("nil_items", func(t *testing.T) {
		var diags diag.Diagnostics
		if out := flattenIPv4Items(nil, &diags); out != nil {
			t.Errorf("ipv4 = %v, want nil", out)
		}
		if out := flattenIPv6Items(nil, &diags); out != nil {
			t.Errorf("ipv6 = %v, want nil", out)
		}
		if out := flattenPortItems(nil, &diags); out != nil {
			t.Errorf("ports = %v, want nil", out)
		}
		if diags.HasError() {
			t.Errorf("nil items should not error: %v", diags)
		}
	})

	// Zero-value matchers have no union payload, so reading their
	// discriminator fails and the item is skipped with a diagnostic.
	t.Run("broken_discriminator", func(t *testing.T) {
		var d4 diag.Diagnostics
		if out := flattenIPv4Items(&[]official.IPv4Matching{{}}, &d4); len(out) != 0 {
			t.Errorf("ipv4 = %v, want empty", out)
		}
		tmlWantErr(t, d4, "Failed to read IPv4 matcher type")

		var d6 diag.Diagnostics
		if out := flattenIPv6Items(&[]official.IPv6Matching{{}}, &d6); len(out) != 0 {
			t.Errorf("ipv6 = %v, want empty", out)
		}
		tmlWantErr(t, d6, "Failed to read IPv6 matcher type")

		var dp diag.Diagnostics
		if out := flattenPortItems(&[]official.PortMatching{{}}, &dp); len(out) != 0 {
			t.Errorf("ports = %v, want empty", out)
		}
		tmlWantErr(t, dp, "Failed to read port matcher type")
	})
}
